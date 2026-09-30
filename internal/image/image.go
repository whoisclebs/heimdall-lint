// Package image reads a schema out of a container image that is already
// present locally, so the environment contract travels with the application
// version: application version <-> environment contract.
//
// It shells out to the Docker CLI (create without starting, cp, rm). Nothing in
// the image is executed, nothing is pulled, and the network is disabled. The
// core never depends on this package: it is used only when --image or an
// image:// reference is given.
package image

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/whoisclebs/heimdall-lint/internal/fsutil"
	"github.com/whoisclebs/heimdall-lint/internal/schema"
)

const (
	// Scheme prefixes schema references that point into an image.
	Scheme = "image://"
	// DefaultSchemaPath is where an image carries its contract.
	DefaultSchemaPath = "/env.schema.yaml"
	// SchemaLabel overrides DefaultSchemaPath: LABEL io.heimdall.schema=/etc/app/schema.yaml
	SchemaLabel = "io.heimdall.schema"

	maxArchiveBytes = fsutil.MaxSchemaBytes + 64<<10 // tar headers and padding
	cleanupTimeout  = 15 * time.Second
)

// IsReference reports whether a schema path points into an image.
func IsReference(path string) bool { return strings.HasPrefix(path, Scheme) }

// Reference builds the schema path for an image name.
func Reference(image string) string { return Scheme + image }

var (
	imageName     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@-]{0,254}$`)
	containerID   = regexp.MustCompile(`^[0-9a-f]{12,64}$`)
	unsafePathRun = regexp.MustCompile(`[\x00-\x1f]`)
)

// Runner runs the docker CLI. It exists so the flow can be tested without Docker.
type Runner interface {
	Run(ctx context.Context, args ...string) (stdout []byte, stderr string, err error)
}

// Extractor reads schemas from images.
type Extractor struct{ Runner Runner }

// New returns an Extractor backed by the real docker CLI.
func New() Extractor { return Extractor{Runner: dockerRunner{}} }

// Load resolves a schema path: image:// references are read from the image,
// anything else is a file. It has the signature batch and the CLI expect.
func (e Extractor) Load(ctx context.Context) func(path string) (*schema.Schema, error) {
	return func(path string) (*schema.Schema, error) {
		if !IsReference(path) {
			return schema.Load(path)
		}
		data, err := e.Schema(ctx, strings.TrimPrefix(path, Scheme))
		if err != nil {
			var missing *schema.NotFoundError
			if errors.As(err, &missing) {
				return nil, &schema.NotFoundError{Path: path}
			}
			return nil, &schema.Error{Path: path, Problems: []schema.Problem{{Message: err.Error()}}}
		}
		return schema.Parse(data, path)
	}
}

// Schema returns the raw schema bytes stored in the image.
func (e Extractor) Schema(ctx context.Context, ref string) ([]byte, error) {
	if !imageName.MatchString(ref) {
		return nil, fmt.Errorf("invalid image reference %q", ref)
	}

	schemaPath, err := e.schemaPath(ctx, ref)
	if err != nil {
		return nil, err
	}

	stdout, stderr, err := e.Runner.Run(ctx, "create", "--pull", "never", "--network", "none", "--entrypoint", "/heimdall-never-runs", ref)
	if err != nil {
		return nil, dockerError("cannot create a container from "+ref+" (is the image present locally?)", stderr, err)
	}
	id := strings.TrimSpace(string(stdout))
	if !containerID.MatchString(id) {
		return nil, fmt.Errorf("docker returned an unexpected container id")
	}
	defer e.remove(id)

	archive, stderr, err := e.Runner.Run(ctx, "cp", id+":"+schemaPath, "-")
	if err != nil {
		if strings.Contains(stderr, "Could not find the file") || strings.Contains(stderr, "No such container:path") {
			return nil, &schema.NotFoundError{Path: schemaPath}
		}
		return nil, dockerError("cannot copy "+schemaPath+" out of "+ref, stderr, err)
	}
	return readSingleFile(archive)
}

// schemaPath honors the io.heimdall.schema label, defaulting to /env.schema.yaml.
func (e Extractor) schemaPath(ctx context.Context, ref string) (string, error) {
	format := fmt.Sprintf(`{{index .Config.Labels %q}}`, SchemaLabel)
	stdout, stderr, err := e.Runner.Run(ctx, "image", "inspect", "--format", format, ref)
	if err != nil {
		return "", dockerError("cannot inspect image "+ref+" (is it present locally?)", stderr, err)
	}
	path := strings.TrimSpace(string(stdout))
	switch {
	case path == "" || path == "<no value>":
		return DefaultSchemaPath, nil
	case !strings.HasPrefix(path, "/") || unsafePathRun.MatchString(path):
		return "", fmt.Errorf("label %s must be an absolute path", SchemaLabel)
	}
	return path, nil
}

// remove deletes the temporary container even when the run was cancelled.
func (e Extractor) remove(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	_, _, _ = e.Runner.Run(ctx, "rm", "-f", id)
}

// readSingleFile extracts the one regular file docker cp streams as a tar. A
// directory or oversized file is refused.
func readSingleFile(archive []byte) ([]byte, error) {
	reader := tar.NewReader(bytes.NewReader(archive))
	header, err := reader.Next()
	if err != nil {
		return nil, fmt.Errorf("unexpected output from docker cp: %w", err)
	}
	if header.Typeflag != tar.TypeReg {
		return nil, errors.New("the schema path in the image is not a regular file")
	}
	if header.Size > fsutil.MaxSchemaBytes {
		return nil, fmt.Errorf("schema in the image is larger than %d bytes", fsutil.MaxSchemaBytes)
	}
	return io.ReadAll(io.LimitReader(reader, fsutil.MaxSchemaBytes+1))
}

func dockerError(context, stderr string, err error) error {
	detail := strings.TrimSpace(firstLine(stderr))
	if detail == "" {
		detail = err.Error()
	}
	return fmt.Errorf("%s: %s", context, detail)
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return line
}

// dockerRunner executes the docker binary without a shell, capping the output
// so a hostile image cannot exhaust memory.
type dockerRunner struct{}

func (dockerRunner) Run(ctx context.Context, args ...string) ([]byte, string, error) {
	command := exec.CommandContext(ctx, "docker", args...)
	stdout := &cappedBuffer{limit: maxArchiveBytes}
	var stderr bytes.Buffer
	command.Stdout, command.Stderr = stdout, &limitedStderr{buffer: &stderr}
	err := command.Run()
	if stdout.exceeded {
		return nil, stderr.String(), errors.New("output from docker is too large")
	}
	return stdout.Bytes(), stderr.String(), err
}

// cappedBuffer stops accepting data past limit and remembers that it did.
type cappedBuffer struct {
	bytes.Buffer
	limit    int
	exceeded bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		b.exceeded = true
		return 0, errors.New("output limit exceeded")
	}
	return b.Buffer.Write(p)
}

// limitedStderr keeps the first few KiB of stderr, which is all an error needs.
type limitedStderr struct{ buffer *bytes.Buffer }

func (l *limitedStderr) Write(p []byte) (int, error) {
	if room := 8<<10 - l.buffer.Len(); room > 0 {
		l.buffer.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}
