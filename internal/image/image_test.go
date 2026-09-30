package image

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/whoisclebs/heimdall-lint/internal/fsutil"
	"github.com/whoisclebs/heimdall-lint/internal/schema"
)

const validSchema = "version: 1\nvariables:\n  A: {type: integer, required: true}\n"

type reply struct {
	stdout []byte
	stderr string
	err    error
}

// fakeRunner answers docker subcommands and records every call.
type fakeRunner struct {
	replies map[string]reply // keyed by first argument(s): "image inspect", "create", "cp", "rm"
	calls   [][]string
}

func (f *fakeRunner) Run(_ context.Context, args ...string) ([]byte, string, error) {
	f.calls = append(f.calls, args)
	key := args[0]
	if key == "image" {
		key = "image inspect"
	}
	r := f.replies[key]
	return r.stdout, r.stderr, r.err
}

func (f *fakeRunner) called(name string) bool {
	for _, call := range f.calls {
		if call[0] == name {
			return true
		}
	}
	return false
}

func tarOf(t *testing.T, name string, typeflag byte, content string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	header := &tar.Header{Name: name, Typeflag: typeflag, Mode: 0o644, Size: int64(len(content))}
	if typeflag == tar.TypeDir {
		header.Size = 0
	}
	if err := writer.WriteHeader(header); err != nil {
		t.Fatal(err)
	}
	if typeflag == tar.TypeReg {
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func happyRunner(t *testing.T) *fakeRunner {
	return &fakeRunner{replies: map[string]reply{
		"image inspect": {stdout: []byte("\n")},
		"create":        {stdout: []byte("0123456789abcdef0123\n")},
		"cp":            {stdout: tarOf(t, "env.schema.yaml", tar.TypeReg, validSchema)},
	}}
}

func TestSchemaReadsTheFileAndCleansUp(t *testing.T) {
	runner := happyRunner(t)
	data, err := Extractor{Runner: runner}.Schema(context.Background(), "company/reports-api:2026.10.0")
	if err != nil || string(data) != validSchema {
		t.Fatalf("data = %q, err = %v", data, err)
	}

	var create, cp, rm []string
	for _, call := range runner.calls {
		switch call[0] {
		case "create":
			create = call
		case "cp":
			cp = call
		case "rm":
			rm = call
		}
	}
	for _, want := range []string{"--pull", "never", "--network", "none", "--entrypoint"} {
		if !contains(create, want) {
			t.Errorf("create lacks %s: %v (nothing may run, be pulled, or touch the network)", want, create)
		}
	}
	if cp[1] != "0123456789abcdef0123:/env.schema.yaml" {
		t.Errorf("cp = %v", cp)
	}
	if len(rm) == 0 || rm[2] != "0123456789abcdef0123" {
		t.Errorf("container not removed: %v", runner.calls)
	}
}

func contains(list []string, item string) bool {
	for _, entry := range list {
		if entry == item {
			return true
		}
	}
	return false
}

func TestSchemaPathLabel(t *testing.T) {
	runner := happyRunner(t)
	runner.replies["image inspect"] = reply{stdout: []byte("/etc/app/schema.yaml\n")}
	if _, err := (Extractor{Runner: runner}).Schema(context.Background(), "app:1"); err != nil {
		t.Fatal(err)
	}
	for _, call := range runner.calls {
		if call[0] == "cp" && call[1] != "0123456789abcdef0123:/etc/app/schema.yaml" {
			t.Fatalf("cp = %v", call)
		}
	}

	for _, label := range []string{"relative/path.yaml", "/bad\npath"} {
		runner = happyRunner(t)
		runner.replies["image inspect"] = reply{stdout: []byte(label + "\n")}
		if _, err := (Extractor{Runner: runner}).Schema(context.Background(), "app:1"); err == nil {
			t.Errorf("label %q accepted", label)
		}
		if runner.called("create") {
			t.Errorf("a container was created despite a bad label %q", label)
		}
	}
}

func TestSchemaRejectsHostileReferences(t *testing.T) {
	for _, ref := range []string{"", "-v /:/host", "--privileged", "a b", "a;rm -rf /", "$(id)", strings.Repeat("a", 300), "img\nname"} {
		runner := happyRunner(t)
		if _, err := (Extractor{Runner: runner}).Schema(context.Background(), ref); err == nil {
			t.Errorf("reference %q accepted", ref)
		}
		if len(runner.calls) != 0 {
			t.Errorf("docker was invoked for %q", ref)
		}
	}
	for _, ref := range []string{"nginx", "company/reports-api:2026.10.0", "registry.local:5000/team/app@sha256:abc123", "app_1.2-rc"} {
		if _, err := (Extractor{Runner: happyRunner(t)}).Schema(context.Background(), ref); err != nil {
			t.Errorf("reference %q rejected: %v", ref, err)
		}
	}
}

func TestSchemaErrors(t *testing.T) {
	failure := errors.New("exit status 1")

	runner := happyRunner(t)
	runner.replies["image inspect"] = reply{stderr: "Error: No such image: ghost:1\n", err: failure}
	_, err := Extractor{Runner: runner}.Schema(context.Background(), "ghost:1")
	if err == nil || !strings.Contains(err.Error(), "No such image") || !strings.Contains(err.Error(), "present locally") {
		t.Errorf("missing image: %v", err)
	}

	runner = happyRunner(t)
	runner.replies["create"] = reply{stderr: "boom\nsecond line", err: failure}
	if _, err = (Extractor{Runner: runner}).Schema(context.Background(), "app:1"); err == nil || !strings.Contains(err.Error(), "boom") || strings.Contains(err.Error(), "second line") {
		t.Errorf("create failure: %v", err)
	}
	if runner.called("rm") {
		t.Error("rm must not run when no container was created")
	}

	runner = happyRunner(t)
	runner.replies["create"] = reply{stdout: []byte("not-an-id; rm -rf /")}
	if _, err = (Extractor{Runner: runner}).Schema(context.Background(), "app:1"); err == nil {
		t.Error("garbage container id accepted")
	}
}

func TestMissingSchemaInImageIsNotFoundAndStillCleansUp(t *testing.T) {
	runner := happyRunner(t)
	runner.replies["cp"] = reply{stderr: "Error response from daemon: Could not find the file /env.schema.yaml in container abc", err: errors.New("exit status 1")}
	_, err := Extractor{Runner: runner}.Schema(context.Background(), "app:1")
	var missing *schema.NotFoundError
	if !errors.As(err, &missing) || missing.Path != DefaultSchemaPath {
		t.Fatalf("err = %v", err)
	}
	if !runner.called("rm") {
		t.Fatal("container leaked after a failed cp")
	}
}

func TestReadSingleFileRefusesUnsafeArchives(t *testing.T) {
	if _, err := readSingleFile(tarOf(t, "etc/", tar.TypeDir, "")); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("directory: %v", err)
	}
	if _, err := readSingleFile([]byte("not a tar")); err == nil {
		t.Error("garbage accepted")
	}
	if _, err := readSingleFile(nil); err == nil {
		t.Error("empty output accepted")
	}
	huge := strings.Repeat("A", fsutil.MaxSchemaBytes+1)
	if _, err := readSingleFile(tarOf(t, "env.schema.yaml", tar.TypeReg, huge)); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("oversized file: %v", err)
	}
}

func TestLoad(t *testing.T) {
	extractor := Extractor{Runner: happyRunner(t)}
	load := extractor.Load(context.Background())

	parsed, err := load(Reference("app:1"))
	if err != nil || parsed.Path != "image://app:1" || parsed.Variables["A"] == nil {
		t.Fatalf("parsed = %+v, err = %v", parsed, err)
	}

	broken := happyRunner(t)
	broken.replies["cp"] = reply{stdout: tarOf(t, "s", tar.TypeReg, "version: 1\nvariables:\n  A: {type: banana}\n")}
	var invalid *schema.Error
	if _, err := (Extractor{Runner: broken}).Load(context.Background())(Reference("app:1")); !errors.As(err, &invalid) || invalid.Path != "image://app:1" {
		t.Errorf("an invalid schema inside an image must be a schema.Error: %v", err)
	}

	missing := happyRunner(t)
	missing.replies["cp"] = reply{stderr: "Could not find the file", err: errors.New("x")}
	var notFound *schema.NotFoundError
	if _, err := (Extractor{Runner: missing}).Load(context.Background())(Reference("app:1")); !errors.As(err, &notFound) || notFound.Path != "image://app:1" {
		t.Errorf("not found must keep the image reference: %v", err)
	}

	if _, err := load("definitely-missing.schema.yaml"); err == nil {
		t.Error("plain paths must still go through schema.Load")
	}
}

func TestReferenceHelpers(t *testing.T) {
	if !IsReference("image://a:1") || IsReference("a.schema.yaml") || Reference("a:1") != "image://a:1" {
		t.Fatal("reference helpers")
	}
}
