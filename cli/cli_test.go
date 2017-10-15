// This file is part of the go-meta library.
//
// Copyright (C) 2017 JAAK MUSIC LTD
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>.
//
// If you have any questions please contact yo@jaak.io

package cli

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ipfs/go-cid"
	"github.com/meta-network/go-meta"
)

// TestCWRCommands tests running the 'meta cwr convert' and
// 'meta cwr index' commands.
func TestCWRCommands(t *testing.T) {
	c, err := newTestCLI(t)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(c.tmpDir)

	// check 'meta cwr convert' prints a CID
	stdout := c.run("cwr", "convert",
		"../cwr/testdata/example_double_nwr.cwr",
		"../cwr/testdata/example_nwr.cwr")
	var ids []string
	s := bufio.NewScanner(strings.NewReader(stdout))
	for s.Scan() {
		id, err := cid.Parse(s.Text())
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id.String())
	}
	expected := []string{
		"zdpuAuzWbTPDY6Bwnt6qF2UFJotovT3VpHVZc76QzK5TW9SKG",
		"zdpuApeC2yq7xUw9HL7dQiduJXv6pDg3UByr9pLaD9P83XnZJ",
	}
	if !reflect.DeepEqual(ids, expected) {
		t.Fatalf("unexpected CIDs:\nexpected: %v\ngot:      %v", expected, ids)
	}

	db := filepath.Join(c.tmpDir, "index.db")

	// run 'meta cwr index' with the CIDs as stdin
	stream := strings.NewReader(stdout)
	c.runWithStdin(stream, "cwr", "index", db)

	// check the index was populated
	cmd := exec.Command("sqlite3", db, "SELECT cwr_id FROM transmission_header ORDER BY cwr_id")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("error checking index: %s: %s", err, out)
	}
	gotIDs := strings.Split(strings.TrimSpace(string(out)), "\n")
	sort.Strings(expected)
	if !reflect.DeepEqual(gotIDs, expected) {
		t.Fatalf("unexpected index output:\nexpected: %v\ngot:      %q", expected, gotIDs)
	}
}

// TestERNCommands tests running the 'meta ern convert' and
// 'meta ern index' commands.
func TestERNCommands(t *testing.T) {
	c, err := newTestCLI(t)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(c.tmpDir)

	// check 'meta ern convert' prints multiple CIDs
	stdout := c.run("ern", "convert",
		"../ern/testdata/Profile_AudioAlbumMusicOnly.xml",
		"../ern/testdata/Profile_AudioAlbum_WithBooklet.xml",
		"../ern/testdata/Profile_AudioBook.xml",
		"../ern/testdata/Profile_AudioSingle.xml",
		"../ern/testdata/Profile_AudioSingle_WithCompoundArtistsAndTerritorialOverride.xml",
	)
	var ids []string
	s := bufio.NewScanner(strings.NewReader(stdout))
	for s.Scan() {
		id, err := cid.Parse(s.Text())
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id.String())
	}
	expected := []string{
		"zdpuAqJJKxdPMDU6q4BoFMQjavn6TiNxtFJ9dSgTWJDtGyqLL",
		"zdpuAvQkHEjLxYJvYL1bBA7ri7rWxauj2NqDE7rJKtwEbmG2w",
		"zdpuArVpjL6zsTmemaenfeVCTBCmJYBnz7pjK2SDGJ64EGbR8",
		"zdpuAy8P7JNdYv9Y1CD8dBeY3Pwjm5PzpLbSV3KQgyLcw1JiS",
		"zdpuB1bfxL28n5Bgx9vsG6huuYttNGSigXVACk18K17BTYtYT",
	}
	if !reflect.DeepEqual(ids, expected) {
		t.Fatalf("unexpected CIDs:\nexpected: %v\ngot:      %v", expected, ids)
	}

	db := filepath.Join(c.tmpDir, "index.db")

	// run 'meta ern index' with the CIDs as stdin
	stream := strings.NewReader(stdout)
	c.runWithStdin(stream, "ern", "index", db)

	// check the index was populated
	cmd := exec.Command("sqlite3", db, "SELECT cid FROM ern ORDER BY cid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("error checking index: %s: %s", err, out)
	}
	gotIDs := strings.Split(strings.TrimSpace(string(out)), "\n")
	sort.Strings(expected)
	if !reflect.DeepEqual(gotIDs, expected) {
		t.Fatalf("unexpected index output:\nexpected: %v\ngot:      %q", expected, gotIDs)
	}
}

// TestERNCommands tests running the 'meta eidr convert' and
// 'meta eidr index' commands.
func TestEIDRCommands(t *testing.T) {
	c, err := newTestCLI(t)
	if err != nil {
		t.Fatal(err)
	}

	// check 'meta eidr convert' outputs expected rows
	stdout := c.run("eidr", "convert",
		"../eidr/testdata/dummy_child.xml",
		"../eidr/testdata/dummy_parent.xml",
	)
	var ids []string
	s := bufio.NewScanner(strings.NewReader(stdout))
	for s.Scan() {
		id, err := cid.Parse(s.Text())
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id.String())
	}
	expected := []string{
		"zdpuB2humiaiBKwWAQHfXFhPc9UheUwsdrrkYJPfMBXdFnVEm",
		"zdpuAukxJjQz3P67xqLiEFfx8YoFtmnQJ9ZfhKnddrS77tNTM",
	}
	if !reflect.DeepEqual(ids, expected) {
		t.Fatalf("unexpected CIDs:\nexpected: %v\ngot:      %v", expected, ids)
	}

	// create a path to store the index
	tmpDir, err := ioutil.TempDir("", "meta-main-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)
	db := filepath.Join(tmpDir, "index.db")

	// run 'meta ern index' with the CIDs as stdin
	stream := strings.NewReader(stdout)
	c.runWithStdin(stream, "eidr", "index", db)

	// check if the index has the baseobject and xobject
	cmd := exec.Command("sqlite3", db, "select count(*) from xobject_baseobject_link x inner join baseobject p, xobject_episode e on p.doi_id = x.parent_doi_id where e.id = x.xobject_id")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("error checking index: %s: %s", err, out)
	}
	if strings.TrimSpace(string(out)) != "1" {
		t.Fatalf("baseobject/xobject link count mismatch; expected 1, got %s", out)
	}

	// check if associatedorgs are inserted and linked
	cmd = exec.Command("sqlite3", db, "select count(*) from org o inner join baseobject b on o.base_doi_id = b.doi_id")
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("error checking index: %s: %s", err, out)
	}
	if strings.TrimSpace(string(out)) != "2" {
		t.Fatalf("associatedorg link count mismatch; expected 2, got %s", out)
	}

	// check if alternateids are inserted and linked
	cmd = exec.Command("sqlite3", db, "select count(*) from alternateid a inner join baseobject b on a.base_doi_id = b.doi_id")
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("error checking index: %s: %s", err, out)
	}
	if strings.TrimSpace(string(out)) != "2" {
		t.Fatalf("alternateid link count mismatch; expected 2, got %s", out)
	}
}

type testCLI struct {
	t      *testing.T
	store  *meta.Store
	tmpDir string
}

func newTestCLI(t *testing.T) (*testCLI, error) {
	// create a path to store the index and to store the meta objects.
	tmpDir, err := ioutil.TempDir("", "meta-main-test")
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			os.RemoveAll(tmpDir)
		}
	}()
	store, err := meta.NewFSStore(tmpDir)
	if err != nil {
		return nil, err
	}
	return &testCLI{
		t:      t,
		store:  store,
		tmpDir: tmpDir,
	}, nil
}

func (c *testCLI) runWithStdin(stdin io.Reader, args ...string) string {
	var stdout bytes.Buffer
	cli := New(c.store, stdin, &stdout)
	if err := cli.Run(context.Background(), args...); err != nil {
		c.t.Fatal(err)
	}
	return stdout.String()
}

func (c *testCLI) run(args ...string) string {
	return c.runWithStdin(nil, args...)
}
