package main

import (
	"testing"

	"github.com/KnifeLemon/Droidline/spec"
)

func TestPathIsLocalOnlyWhenClientOnly(t *testing.T) {
	sp := spec.MustLoad()
	build := func(name string, args ...string) (map[string]any, string) {
		t.Helper()
		cmd, by := sp.Command(name)
		req, local, err := buildRequest(sp, cmd, by, name, args)
		if err != nil {
			t.Fatal(err)
		}
		return req, local
	}
	if req, local := build("files", "Download", "--sort", "time"); req["path"] != "Download" || local != "" {
		t.Fatalf("files sent %v, local %q", req, local)
	}
	if req, local := build("pull", "Download/a.pdf", "out.pdf"); req["file"] != "Download/a.pdf" || req["path"] != nil || local != "out.pdf" {
		t.Fatalf("pull sent %v, local %q", req, local)
	}
	if req, local := build("pull", "Download/a.pdf"); req["format"] != nil || local != "" {
		t.Fatalf("pull without a local path sent %v, local %q", req, local)
	}
	if req, local := build("screenshot", "shot.png"); req["format"] != "png" || local != "shot.png" {
		t.Fatalf("screenshot sent %v, local %q", req, local)
	}
}
