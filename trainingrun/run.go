// Package trainingrun publishes, verifies, and promotes training runs: a
// directory holding a model artifact, its training report, its canonical
// configuration, and a manifest pix-risk-run-v1 that binds them by SHA-256,
// as specified in docs/contracts/pix-risk-model-v1.md. A run is published
// by renaming a fully written and verified staging directory, so it exists
// completely or not at all (ADR 0016).
package trainingrun

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"unicode/utf8"

	"github.com/digocelos/transaction-risk-go/internal/strictjson"
	"github.com/digocelos/transaction-risk-go/modelartifact"
	"github.com/digocelos/transaction-risk-go/trainingconfig"
	"github.com/digocelos/transaction-risk-go/trainingreport"
)

// ManifestVersion is the version of the run manifest.
const ManifestVersion = "pix-risk-run-v1"

// Files of a run.
const (
	ModelFile    = "model.json"
	ReportFile   = "report.json"
	ConfigFile   = "config.json"
	ManifestFile = "manifest.json"
)

// ErrExists reports a run directory or a promotion target that already
// exists and must not be overwritten.
var ErrExists = errors.New("already exists")

// Bundle holds the files of a run: the encoded model artifact, the exact
// report bytes, and the canonical configuration.
type Bundle struct {
	Model  []byte
	Report []byte
	Config []byte
}

// Manifest binds the files of a run by their SHA-256 and size.
type Manifest struct {
	ManifestVersion string `json:"manifest_version"`
	Files           []File `json:"files"`
}

// File is one manifest entry.
type File struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

// Run is a verified run.
type Run struct {
	Dir      string
	Manifest Manifest
	Artifact modelartifact.Artifact
	Config   trainingconfig.Config
	Bundle   Bundle
}

// filesystem holds the operations Publish and Promote use, so tests can
// inject failures.
var filesystem = struct {
	mkdirTemp func(dir, pattern string) (string, error)
	writeFile func(path string, data []byte) error
	rename    func(from, to string) error
}{
	mkdirTemp: os.MkdirTemp,
	writeFile: writeSynced,
	rename:    os.Rename,
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// NewManifest describes a bundle.
func NewManifest(bundle Bundle) Manifest {
	entry := func(path string, data []byte) File {
		return File{Path: path, SHA256: sha256Hex(data), Bytes: int64(len(data))}
	}

	return Manifest{
		ManifestVersion: ManifestVersion,
		Files: []File{
			entry(ModelFile, bundle.Model),
			entry(ReportFile, bundle.Report),
			entry(ConfigFile, bundle.Config),
		},
	}
}

// Publish verifies a bundle, writes it with its manifest into a staging
// directory next to dir, verifies the files written, and renames the
// staging directory to dir. dir must not exist; Publish never overwrites a
// run. On any failure, no run is published and the staging directory is
// removed.
func Publish(dir string, bundle Bundle) error {
	if _, err := verifyBundle(bundle); err != nil {
		return fmt.Errorf("refusing to publish an inconsistent run: %w", err)
	}

	if _, err := os.Lstat(dir); err == nil {
		return fmt.Errorf("run %s %w", dir, ErrExists)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	manifest, err := json.MarshalIndent(NewManifest(bundle), "", "  ")
	if err != nil {
		return err
	}
	manifest = append(manifest, '\n')

	parent, name := filepath.Split(filepath.Clean(dir))
	if parent == "" {
		parent = "."
	}

	staging, err := filesystem.mkdirTemp(parent, "."+name+".staging-*")
	if err != nil {
		return fmt.Errorf("stage run: %w", err)
	}

	published := false
	defer func() {
		if !published {
			os.RemoveAll(staging)
		}
	}()

	// The manifest is written last: a staging directory without one is
	// incomplete.
	for _, file := range []struct {
		name string
		data []byte
	}{
		{ModelFile, bundle.Model},
		{ReportFile, bundle.Report},
		{ConfigFile, bundle.Config},
		{ManifestFile, manifest},
	} {
		if err := filesystem.writeFile(filepath.Join(staging, file.name), file.data); err != nil {
			return fmt.Errorf("stage %s: %w", file.name, err)
		}
	}

	if _, err := Verify(staging); err != nil {
		return fmt.Errorf("verify staged run: %w", err)
	}

	if _, err := os.Lstat(dir); err == nil {
		return fmt.Errorf("run %s %w", dir, ErrExists)
	}

	if err := filesystem.rename(staging, dir); err != nil {
		return fmt.Errorf("publish run: %w", err)
	}
	published = true

	syncDir(parent)
	return nil
}

// Verify reads a run directory and checks it completely: the manifest is
// strict and lists exactly the model, report, and configuration; every
// file matches its SHA-256 and size; the directory holds no other file; the
// artifact and the configuration decode; and the artifact, report, and
// configuration bind each other.
func Verify(dir string) (Run, error) {
	run := Run{Dir: dir}

	manifestData, err := readBounded(filepath.Join(dir, ManifestFile), maxManifestSize)
	if err != nil {
		return Run{}, fmt.Errorf("read manifest: %w", err)
	}

	if err := decodeStrict(manifestData, &run.Manifest); err != nil {
		return Run{}, fmt.Errorf("decode manifest: %w", err)
	}

	if run.Manifest.ManifestVersion != ManifestVersion {
		return Run{}, fmt.Errorf("unsupported manifest version: %q", run.Manifest.ManifestVersion)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return Run{}, err
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if want := []string{ConfigFile, ManifestFile, ModelFile, ReportFile}; !slices.Equal(names, want) {
		return Run{}, fmt.Errorf("a run holds exactly %v, found %v", want, names)
	}

	contents := map[string]*[]byte{ModelFile: &run.Bundle.Model, ReportFile: &run.Bundle.Report, ConfigFile: &run.Bundle.Config}
	if len(run.Manifest.Files) != 3 {
		return Run{}, errors.New("the manifest must list exactly the model, report, and configuration")
	}

	for i, want := range []string{ModelFile, ReportFile, ConfigFile} {
		file := run.Manifest.Files[i]
		if file.Path != want {
			return Run{}, fmt.Errorf("manifest entry %d is %q, want %q", i, file.Path, want)
		}

		if limit, ok := maxFileSizes[file.Path]; ok && file.Bytes > limit {
			return Run{}, fmt.Errorf("%s declares %d bytes, above the limit of %d", file.Path, file.Bytes, limit)
		}

		// Reading one byte past the declared size detects a longer file
		// without reading the rest of it.
		data, err := readBounded(filepath.Join(dir, file.Path), max(file.Bytes, 0)+1)
		if err != nil {
			return Run{}, err
		}

		if sha256Hex(data) != file.SHA256 || int64(len(data)) != file.Bytes {
			return Run{}, fmt.Errorf("%s does not match its manifest entry", file.Path)
		}

		*contents[file.Path] = data
	}

	verified, err := verifyBundle(run.Bundle)
	if err != nil {
		return Run{}, err
	}

	run.Artifact, run.Config = verified.Artifact, verified.Config
	return run, nil
}

// verifyBundle decodes the files of a bundle and checks that they bind
// each other.
func verifyBundle(bundle Bundle) (Run, error) {
	artifact, err := modelartifact.Decode(bundle.Model)
	if err != nil {
		return Run{}, err
	}

	config, err := trainingconfig.Decode(bundle.Config)
	if err != nil {
		return Run{}, fmt.Errorf("decode configuration: %w", err)
	}

	canonical, err := config.Canonical()
	if err != nil {
		return Run{}, err
	}
	configSHA256 := sha256Hex(canonical)

	var report struct {
		Version       string `json:"training_report_version"`
		Configuration struct {
			SHA256 string `json:"config_sha256"`
		} `json:"configuration"`
		Dataset struct {
			SHA256 string `json:"dataset_sha256"`
		} `json:"dataset"`
		Selection struct {
			SelectedEpoch int `json:"selected_epoch"`
			Threshold     struct {
				Policy string  `json:"policy"`
				Value  float64 `json:"value"`
			} `json:"threshold"`
		} `json:"selection"`
	}
	if err := json.Unmarshal(bundle.Report, &report); err != nil {
		return Run{}, fmt.Errorf("decode report: %w", err)
	}

	training := artifact.Training
	switch {
	case !bytes.Equal(bundle.Config, canonical):
		return Run{}, errors.New("the configuration is not in its canonical form")
	case report.Version != trainingreport.Version:
		return Run{}, fmt.Errorf("unsupported report version: %q", report.Version)
	case training.ReportSHA256 != sha256Hex(bundle.Report):
		return Run{}, errors.New("the model does not bind this report")
	case training.ConfigSHA256 != configSHA256 || report.Configuration.SHA256 != configSHA256:
		return Run{}, errors.New("the model, the report, and the configuration do not bind the same configuration")
	case report.Dataset.SHA256 != training.DatasetSHA256:
		return Run{}, errors.New("the model and the report do not name the same dataset")
	case report.Selection.SelectedEpoch != artifact.SelectedEpoch ||
		report.Selection.Threshold.Policy != artifact.Threshold.Policy ||
		report.Selection.Threshold.Value != artifact.Threshold.Value:
		return Run{}, errors.New("the model and the report do not record the same selection")
	}

	return Run{Artifact: artifact, Config: config}, nil
}

// Promote verifies a run and writes its model artifact to output through a
// temporary file and a rename, so output never holds a partial artifact.
// Training never promotes; promotion is this explicit step. Without
// replace, an existing output is never overwritten. It returns the
// SHA-256 of the promoted artifact.
func Promote(runDir, output string, replace bool) (string, error) {
	run, err := Verify(runDir)
	if err != nil {
		return "", fmt.Errorf("verify run: %w", err)
	}

	if _, err := os.Lstat(output); err == nil && !replace {
		return "", fmt.Errorf("%s %w; promote with replace to overwrite it", output, ErrExists)
	}

	temporary, err := filesystem.mkdirTemp(filepath.Dir(output), "."+filepath.Base(output)+".promote-*")
	if err != nil {
		return "", fmt.Errorf("promote: %w", err)
	}
	defer os.RemoveAll(temporary)

	staged := filepath.Join(temporary, ModelFile)
	if err := filesystem.writeFile(staged, run.Bundle.Model); err != nil {
		return "", fmt.Errorf("promote: %w", err)
	}

	if !replace {
		if _, err := os.Lstat(output); err == nil {
			return "", fmt.Errorf("%s %w", output, ErrExists)
		}
	}

	if err := filesystem.rename(staged, output); err != nil {
		return "", fmt.Errorf("promote: %w", err)
	}

	syncDir(filepath.Dir(output))
	return sha256Hex(run.Bundle.Model), nil
}

// maxManifestSize bounds the manifest, which lists three files.
const maxManifestSize = 64 << 10

// maxFileSizes bound the files whose size does not grow with the dataset.
// The report grows with the records of the dataset, so the record limit of
// the run that wrote it bounds it.
var maxFileSizes = map[string]int64{
	ModelFile:  modelartifact.MaxArtifactSize,
	ConfigFile: trainingconfig.MaxDocumentSize,
}

// readBounded reads at most limit bytes of a file.
func readBounded(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	return io.ReadAll(io.LimitReader(file, limit))
}

// writeSynced writes a new file and syncs it before closing.
func writeSynced(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}

	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}

	return err
}

// syncDir syncs a directory so a rename into it is durable. It is best
// effort: some platforms cannot sync directories.
func syncDir(dir string) {
	if handle, err := os.Open(dir); err == nil {
		handle.Sync()
		handle.Close()
	}
}

// decodeStrict rejects invalid UTF-8, duplicate members, trailing data,
// and unknown members before decoding.
func decodeStrict(data []byte, value any) error {
	if !utf8.Valid(data) {
		return errors.New("not valid UTF-8")
	}

	root, duplicates, err := strictjson.Parse(data)
	if err != nil {
		return fmt.Errorf("not valid JSON: %w", err)
	}
	if len(duplicates) > 0 {
		return fmt.Errorf("duplicate member %s", duplicates[0])
	}
	if root.Kind != strictjson.KindObject {
		return errors.New("not a JSON object")
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode(value)
}
