package ci

import "testing"

func TestParseCIConfig(t *testing.T) {
	raw := `
jobs:
  build:
    image: golang:1.22
    steps:
      - run: go test ./...
triggers: [push, pull_request]
`
	cfg, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Jobs["build"].Image != "golang:1.22" {
		t.Fatalf("%#v", cfg.Jobs["build"])
	}
	img, cmds, err := JobCommands(raw, "build")
	if err != nil || img != "golang:1.22" || len(cmds) != 1 {
		t.Fatalf("img=%s cmds=%v err=%v", img, cmds, err)
	}
}
