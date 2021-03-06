# Copyright (C) The Arvados Authors. All rights reserved.
#
# SPDX-License-Identifier: AGPL-3.0

class AddPreserveVersionToCollections < ActiveRecord::Migration
  def change
    add_column :collections, :preserve_version, :boolean, default: false
  end
end
