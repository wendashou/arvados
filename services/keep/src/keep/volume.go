package main

import (
	"crypto/md5"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// A Volume is an interface that represents a Keep back-end volume.

type Volume interface {
	Read(loc string) ([]byte, error)
	Write(loc string, block []byte) error
	Index(prefix string) string
	Status() *VolumeStatus
}

// A UnixVolume is a Volume that writes to a locally mounted disk.
type UnixVolume struct {
	root string // path to this volume
}

// Read retrieves a block identified by the locator string "loc", and
// returns its contents as a byte slice.
//
// If the block could not be opened or read, Read returns a nil slice
// and the os.Error that was generated.
//
// If the block is present but its content hash does not match loc,
// Read returns the block and a CorruptError.  It is the caller's
// responsibility to decide what (if anything) to do with the
// corrupted data block.
//
func (v *UnixVolume) Read(loc string) ([]byte, error) {
	var f *os.File
	var err error
	var nread int

	blockFilename := fmt.Sprintf("%s/%s/%s", v.root, loc[0:3], loc)

	f, err = os.Open(blockFilename)
	if err != nil {
		return nil, err
	}

	var buf = make([]byte, BLOCKSIZE)
	nread, err = f.Read(buf)
	if err != nil {
		log.Printf("%s: reading %s: %s\n", v.root, blockFilename, err)
		return buf, err
	}

	// Double check the file checksum.
	//
	filehash := fmt.Sprintf("%x", md5.Sum(buf[:nread]))
	if filehash != loc {
		// TODO(twp): this condition probably represents a bad disk and
		// should raise major alarm bells for an administrator: e.g.
		// they should be sent directly to an event manager at high
		// priority or logged as urgent problems.
		//
		log.Printf("%s: checksum mismatch: %s (actual locator %s)\n",
			v.root, blockFilename, filehash)
		return buf, CorruptError
	}

	// Success!
	return buf[:nread], nil
}

// Write stores a block of data identified by the locator string
// "loc".  It returns nil on success.  If the volume is full, it
// returns a FullError.  If the write fails due to some other error,
// that error is returned.
//
func (v *UnixVolume) Write(loc string, block []byte) error {
	if v.IsFull() {
		return FullError
	}
	blockDir := fmt.Sprintf("%s/%s", v.root, loc[0:3])
	if err := os.MkdirAll(blockDir, 0755); err != nil {
		log.Printf("%s: could not create directory %s: %s",
			loc, blockDir, err)
		return err
	}

	tmpfile, tmperr := ioutil.TempFile(blockDir, "tmp"+loc)
	if tmperr != nil {
		log.Printf("ioutil.TempFile(%s, tmp%s): %s", blockDir, loc, tmperr)
		return tmperr
	}
	blockFilename := fmt.Sprintf("%s/%s", blockDir, loc)

	if _, err := tmpfile.Write(block); err != nil {
		log.Printf("%s: writing to %s: %s\n", v.root, blockFilename, err)
		return err
	}
	if err := tmpfile.Close(); err != nil {
		log.Printf("closing %s: %s\n", tmpfile.Name(), err)
		os.Remove(tmpfile.Name())
		return err
	}
	if err := os.Rename(tmpfile.Name(), blockFilename); err != nil {
		log.Printf("rename %s %s: %s\n", tmpfile.Name(), blockFilename, err)
		os.Remove(tmpfile.Name())
		return err
	}
	return nil
}

// Status returns a VolumeStatus struct describing the volume's
// current state.
//
func (v *UnixVolume) Status() *VolumeStatus {
	var fs syscall.Statfs_t
	var devnum uint64

	if fi, err := os.Stat(v.root); err == nil {
		devnum = fi.Sys().(*syscall.Stat_t).Dev
	} else {
		log.Printf("%s: os.Stat: %s\n", v.root, err)
		return nil
	}

	err := syscall.Statfs(v.root, &fs)
	if err != nil {
		log.Printf("%s: statfs: %s\n", v.root, err)
		return nil
	}
	// These calculations match the way df calculates disk usage:
	// "free" space is measured by fs.Bavail, but "used" space
	// uses fs.Blocks - fs.Bfree.
	free := fs.Bavail * uint64(fs.Bsize)
	used := (fs.Blocks - fs.Bfree) * uint64(fs.Bsize)
	return &VolumeStatus{v.root, devnum, free, used}
}

// Index returns a list of blocks found on this volume which begin with
// the specified prefix.
//
// The return value is a multiline string (separated by
// newlines). Each line is in the format
//
//     locator+size modification-time
//
// e.g.:
//
//     e4df392f86be161ca6ed3773a962b8f3+67108864 1388894303
//     e4d41e6fd68460e0e3fc18cc746959d2+67108864 1377796043
//     e4de7a2810f5554cd39b36d8ddb132ff+67108864 1388701136
//
func (v *UnixVolume) Index(prefix string) (output string) {
	filepath.Walk(v.root,
		func(path string, info os.FileInfo, err error) error {
			// This WalkFunc inspects each path in the volume
			// and prints an index line for all files that begin
			// with prefix.
			if err != nil {
				log.Printf("IndexHandler: %s: walking to %s: %s",
					v.root, path, err)
				return nil
			}
			locator := filepath.Base(path)
			// Skip directories that do not match prefix.
			// We know there is nothing interesting inside.
			if info.IsDir() &&
				!strings.HasPrefix(locator, prefix) &&
				!strings.HasPrefix(prefix, locator) {
				return filepath.SkipDir
			}
			// Skip any file that is not apparently a locator, e.g. .meta files
			if is_valid, err := IsValidLocator(locator); err != nil {
				return err
			} else if !is_valid {
				return nil
			}
			// Print filenames beginning with prefix
			if !info.IsDir() && strings.HasPrefix(locator, prefix) {
				output = output + fmt.Sprintf(
					"%s+%d %d\n", locator, info.Size(), info.ModTime().Unix())
			}
			return nil
		})

	return
}

// IsFull returns true if the free space on the volume is less than
// MIN_FREE_KILOBYTES.
//
func (v *UnixVolume) IsFull() (isFull bool) {
	fullSymlink := v.root + "/full"

	// Check if the volume has been marked as full in the last hour.
	if link, err := os.Readlink(fullSymlink); err == nil {
		if ts, err := strconv.Atoi(link); err == nil {
			fulltime := time.Unix(int64(ts), 0)
			if time.Since(fulltime).Hours() < 1.0 {
				return true
			}
		}
	}

	if avail, err := v.FreeDiskSpace(); err == nil {
		isFull = avail < MIN_FREE_KILOBYTES
	} else {
		log.Printf("%s: FreeDiskSpace: %s\n", v.root, err)
		isFull = false
	}

	// If the volume is full, timestamp it.
	if isFull {
		now := fmt.Sprintf("%d", time.Now().Unix())
		os.Symlink(now, fullSymlink)
	}
	return
}

// FreeDiskSpace returns the number of unused 1k blocks available on
// the volume.
//
//     TODO(twp): consider integrating this better with VolumeStatus
//     (e.g. keep a NodeStatus object up-to-date periodically and use
//     it as the source of info)
//
func (v *UnixVolume) FreeDiskSpace() (free uint64, err error) {
	var fs syscall.Statfs_t
	err = syscall.Statfs(v.root, &fs)
	if err == nil {
		// Statfs output is not guaranteed to measure free
		// space in terms of 1K blocks.
		free = fs.Bavail * uint64(fs.Bsize) / 1024
	}
	return
}
