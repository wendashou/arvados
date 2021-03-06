// Copyright (C) The Arvados Authors. All rights reserved.
//
// SPDX-License-Identifier: AGPL-3.0

package controller

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"

	"git.curoverse.com/arvados.git/sdk/go/arvados"
	"git.curoverse.com/arvados.git/sdk/go/httpserver"
	"github.com/Sirupsen/logrus"
	check "gopkg.in/check.v1"
)

// logWriter is an io.Writer that writes by calling a "write log"
// function, typically (*check.C)Log().
type logWriter struct {
	logfunc func(...interface{})
}

func (tl *logWriter) Write(buf []byte) (int, error) {
	tl.logfunc(string(bytes.TrimRight(buf, "\n")))
	return len(buf), nil
}

func integrationTestCluster() *arvados.Cluster {
	cfg, err := arvados.GetConfig(filepath.Join(os.Getenv("WORKSPACE"), "tmp", "arvados.yml"))
	if err != nil {
		panic(err)
	}
	cc, err := cfg.GetCluster("zzzzz")
	if err != nil {
		panic(err)
	}
	return cc
}

// Return a new unstarted controller server, using the Rails API
// provided by the integration-testing environment.
func newServerFromIntegrationTestEnv(c *check.C) *httpserver.Server {
	log := logrus.New()
	log.Formatter = &logrus.JSONFormatter{}
	log.Out = &logWriter{c.Log}

	nodeProfile := arvados.NodeProfile{
		Controller: arvados.SystemServiceInstance{Listen: ":"},
		RailsAPI:   arvados.SystemServiceInstance{Listen: os.Getenv("ARVADOS_TEST_API_HOST"), TLS: true, Insecure: true},
	}
	handler := &Handler{Cluster: &arvados.Cluster{
		ClusterID:  "zzzzz",
		PostgreSQL: integrationTestCluster().PostgreSQL,
		NodeProfiles: map[string]arvados.NodeProfile{
			"*": nodeProfile,
		},
	}, NodeProfile: &nodeProfile}

	srv := &httpserver.Server{
		Server: http.Server{
			Handler: httpserver.AddRequestIDs(httpserver.LogRequests(log, handler)),
		},
		Addr: nodeProfile.Controller.Listen,
	}
	return srv
}
