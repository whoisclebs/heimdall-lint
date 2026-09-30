// Package spring imports Spring Boot configuration metadata and turns it into
// a Heimdall schema. It is deliberately decoupled: nothing outside this package
// knows about Spring's file layout.
package spring

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const maxMetadataBytes = 16 << 20

// metadataEntries are the locations of the metadata file, in order of
// preference: a plain jar/directory, then a Spring Boot executable jar.
var metadataEntries = []string{
	"META-INF/spring-configuration-metadata.json",
	"BOOT-INF/classes/META-INF/spring-configuration-metadata.json",
}

type Deprecation struct {
	Level       string `json:"level"`
	Reason      string `json:"reason"`
	Replacement string `json:"replacement"`
}

type Property struct {
	Name         string       `json:"name"`
	Type         string       `json:"type"`
	Description  string       `json:"description"`
	DefaultValue any          `json:"defaultValue"`
	Deprecation  *Deprecation `json:"deprecation"`
}

type HintValue struct {
	Value any `json:"value"`
}

type Hint struct {
	Name   string      `json:"name"`
	Values []HintValue `json:"values"`
}

type Metadata struct {
	Properties []Property `json:"properties"`
	Hints      []Hint     `json:"hints"`
}

// Load reads metadata from a .json file, a .jar/.zip archive, or a directory
// (such as target/classes) containing META-INF/spring-configuration-metadata.json.
func Load(source string) (*Metadata, error) {
	info, err := os.Stat(source)
	if err != nil {
		return nil, err
	}

	var data []byte
	switch {
	case info.IsDir():
		data, err = fromDirectory(source)
	case strings.HasSuffix(strings.ToLower(source), ".jar"), strings.HasSuffix(strings.ToLower(source), ".zip"):
		data, err = fromArchive(source)
	default:
		data, err = readLimited(source)
	}
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse decodes metadata JSON.
func Parse(data []byte) (*Metadata, error) {
	var metadata Metadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, fmt.Errorf("invalid spring-configuration-metadata.json: %w", err)
	}
	if len(metadata.Properties) == 0 {
		return nil, errors.New("metadata declares no properties")
	}
	return &metadata, nil
}

func fromDirectory(dir string) ([]byte, error) {
	for _, entry := range metadataEntries {
		if data, err := readLimited(filepath.Join(dir, filepath.FromSlash(entry))); err == nil {
			return data, nil
		}
	}
	return nil, fmt.Errorf("no %s found in %s", metadataEntries[0], dir)
}

func readLimited(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return readAtMost(file)
}

func readAtMost(reader io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxMetadataBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxMetadataBytes {
		return nil, fmt.Errorf("metadata is larger than %d bytes", maxMetadataBytes)
	}
	return data, nil
}

// fromArchive reads the metadata entry from a jar. The entry's declared size is
// checked and the read is capped, so a zip bomb cannot exhaust memory. Nested
// jars (BOOT-INF/lib) are not opened: only the application's own metadata is read.
func fromArchive(path string) ([]byte, error) {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("cannot open %s as a jar: %w", path, err)
	}
	defer archive.Close()

	for _, wanted := range metadataEntries {
		for _, file := range archive.File {
			if file.Name != wanted {
				continue
			}
			if file.UncompressedSize64 > maxMetadataBytes {
				return nil, fmt.Errorf("%s is larger than %d bytes", wanted, maxMetadataBytes)
			}
			reader, err := file.Open()
			if err != nil {
				return nil, err
			}
			defer reader.Close()
			return readAtMost(reader)
		}
	}
	return nil, fmt.Errorf("%s not found in %s; add spring-boot-configuration-processor to the build", metadataEntries[0], path)
}
