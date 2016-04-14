// -*- Mode: Go; indent-tabs-mode: t -*-

/*
 * Copyright (C) 2016 Canonical Ltd
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License version 3 as
 * published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>.
 *
 */

package client_test

import (
	"io/ioutil"
	"os"
	"path/filepath"

	"gopkg.in/check.v1"

	"github.com/ubuntu-core/snappy/client"
	"github.com/ubuntu-core/snappy/osutil"
)

func (cs *clientSuite) TestClientLogin(c *check.C) {
	cs.rsp = `{"type": "sync", "result":
                     {"username": "the-user-name",
                      "macaroon": "the-root-macaroon",
                      "discharges": ["discharge-macaroon"]}}`

	home := os.Getenv("HOME")
	tmpdir := c.MkDir()
	os.Setenv("HOME", tmpdir)
	defer os.Setenv("HOME", home)

	user, err := cs.cli.Login("username", "pass", "")

	c.Check(err, check.IsNil)
	c.Check(user, check.DeepEquals, &client.User{
		Macaroon:   "the-root-macaroon",
		Discharges: []string{"discharge-macaroon"}})

	outFile := filepath.Join(tmpdir, ".snap", "auth.json")
	c.Check(osutil.FileExists(outFile), check.Equals, true)
	content, err := ioutil.ReadFile(outFile)
	c.Check(err, check.IsNil)
	c.Check(string(content), check.Equals, `{"macaroon":"the-root-macaroon","discharges":["discharge-macaroon"]}`)
}

func (cs *clientSuite) TestClientLoginError(c *check.C) {
	cs.rsp = `{
		"result": {},
		"status": "Bad Request",
		"status_code": 400,
		"type": "error"
	}`

	home := os.Getenv("HOME")
	tmpdir := c.MkDir()
	os.Setenv("HOME", tmpdir)
	defer os.Setenv("HOME", home)
	user, err := cs.cli.Login("username", "pass", "")

	c.Check(user, check.IsNil)
	c.Check(err, check.NotNil)

	outFile := filepath.Join(tmpdir, ".snap", "auth.json")
	c.Check(osutil.FileExists(outFile), check.Equals, false)
}

func (cs *clientSuite) TestWriteAuthData(c *check.C) {
	home := os.Getenv("HOME")
	tmpdir := c.MkDir()
	os.Setenv("HOME", tmpdir)
	defer os.Setenv("HOME", home)

	authData := client.User{
		Macaroon:   "macaroon",
		Discharges: []string{"discharge"},
	}
	err := client.TestWriteAuth(authData)
	c.Assert(err, check.IsNil)

	outFile := filepath.Join(tmpdir, ".snap", "auth.json")
	c.Check(osutil.FileExists(outFile), check.Equals, true)
	content, err := ioutil.ReadFile(outFile)
	c.Check(err, check.IsNil)
	c.Check(string(content), check.Equals, `{"macaroon":"macaroon","discharges":["discharge"]}`)
}

func (cs *clientSuite) TestReadAuthData(c *check.C) {
	home := os.Getenv("HOME")
	tmpdir := c.MkDir()
	os.Setenv("HOME", tmpdir)
	defer os.Setenv("HOME", home)

	authData := client.User{
		Macaroon:   "macaroon",
		Discharges: []string{"discharge"},
	}
	err := client.TestWriteAuth(authData)
	c.Assert(err, check.IsNil)

	readUser, err := client.TestReadAuth()
	c.Assert(err, check.IsNil)
	c.Check(readUser, check.DeepEquals, &authData)
}
