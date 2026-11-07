// Package install downloads, unpacks and runs toolbox CLIs.
package install

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/haikuotianko/toolbox/internal/catalog"
	"github.com/haikuotianko/toolbox/internal/store"
)

// MaxOutput caps one captured stream.
const MaxOutput = 200_000

// Output is one finished process run.
type Output struct {
	ExitCode  int    `json:"exitCode"`
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	Truncated bool   `json:"truncated"`
}

// Result is one successful install.
type Result struct {
	Manifest store.Manifest
	Attempts int
	Failures []string
}

// InstallTool installs a catalog entry, trying its sources in order.
func InstallTool(ctx context.Context, root string, tool catalog.Tool) (Result, error) {
	return InstallToolWithProgress(ctx, root, tool, nil)
}

// InstallToolWithProgress is InstallTool with phase/byte reporting.
func InstallToolWithProgress(ctx context.Context, root string, tool catalog.Tool, progress ProgressFunc) (Result, error) {
	sources := tool.SourcesFor(runtime.GOOS, runtime.GOARCH)
	if len(sources) == 0 {
		return Result{}, fmt.Errorf("no download source for %s/%s and tool %q; add one to <root>/catalog.json or pass a url", runtime.GOOS, runtime.GOARCH, tool.ID)
	}
	return installFrom(ctx, root, tool, sources, progress)
}

// InstallCustom installs from an explicit URL, bypassing the catalog sources.
func InstallCustom(ctx context.Context, root, id, url, archive string) (Result, error) {
	return InstallCustomWithProgress(ctx, root, id, url, archive, nil)
}

// InstallCustomWithProgress is InstallCustom with phase/byte reporting.
func InstallCustomWithProgress(ctx context.Context, root, id, url, archive string, progress ProgressFunc) (Result, error) {
	if !store.ValidID(id) {
		return Result{}, fmt.Errorf("invalid tool id: %q", id)
	}
	tool := catalog.Tool{
		ID:       id,
		Title:    id,
		Binaries: []string{id},
		Sources:  []catalog.Source{{URL: url, Archive: archive}},
	}
	return installFrom(ctx, root, tool, tool.SourcesFor(runtime.GOOS, runtime.GOARCH), progress)
}

// ImportFile takes a local executable the user dropped or picked.
func ImportFile(ctx context.Context, root, id, path string) (Result, error) {
	if !store.ValidID(id) {
		return Result{}, fmt.Errorf("invalid tool id: %q", id)
	}
	info, err := os.Stat(path)
	if err != nil {
		return Result{}, err
	}
	if info.IsDir() {
		return Result{}, fmt.Errorf("%s is a directory", path)
	}
	binDir := store.BinDir(root, id)
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return Result{}, err
	}
	target := filepath.Join(binDir, exeName(filepath.Base(path)))
	if err := copyFile(path, target); err != nil {
		return Result{}, err
	}
	version := probeVersion(ctx, target)
	manifest := store.Manifest{
		ID:          id,
		Title:       id,
		Description: "User-imported local program.",
		Usage:       `toolbox_run { tool: "` + id + `", args: ["--help"] }`,
		Kind:        "binary",
		Binaries:    []string{strings.TrimSuffix(filepath.Base(target), ".exe")},
		Command:     target,
		Version:     version,
		Source:      "import:" + path,
		SizeBytes:   info.Size(),
	}
	if err := store.Save(root, &manifest); err != nil {
		return Result{}, err
	}
	_ = store.WriteReadme(root, &manifest)
	return Result{Manifest: manifest, Attempts: 1}, nil
}

// ScriptSpec is one archived script.
type ScriptSpec struct {
	ID          string
	Title       string
	Description string
	Usage       string
	Language    string
	Content     string
	Command     string
}

// SaveScript archives a reusable script into the toolbox.
func SaveScript(root string, spec ScriptSpec) (store.Manifest, error) {
	if !store.ValidID(spec.ID) {
		return store.Manifest{}, fmt.Errorf("invalid tool id: %q", spec.ID)
	}
	extension := map[string]string{"python": "py", "node": "mjs", "powershell": "ps1"}[strings.ToLower(spec.Language)]
	if extension == "" {
		return store.Manifest{}, fmt.Errorf("unsupported language %q (python | node | powershell)", spec.Language)
	}
	interpreter := spec.Command
	if interpreter == "" {
		switch strings.ToLower(spec.Language) {
		case "node":
			interpreter = "node"
		case "powershell":
			// Windows ships Windows PowerShell 5.1 as powershell.exe; pwsh is an
			// optional install. Pick whichever exists instead of hardcoding pwsh.
			if path, err := exec.LookPath("pwsh"); err == nil {
				interpreter = path
			} else if path, err := exec.LookPath("powershell.exe"); err == nil {
				interpreter = path
			} else {
				interpreter = "powershell.exe"
			}
		default:
			// DSH ships its own Python runtime; "python" is often absent from PATH.
			if path, err := exec.LookPath("python"); err == nil {
				interpreter = path
			} else if path, err := exec.LookPath("py"); err == nil {
				interpreter = path
			} else {
				interpreter = findBundledPython()
			}
		}
	}
	dir := store.ToolDir(root, spec.ID)
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		return store.Manifest{}, err
	}
	scriptPath := filepath.Join(dir, spec.ID+"."+extension)
	body := spec.Content
	if strings.ToLower(spec.Language) == "powershell" && !strings.HasPrefix(body, "\uFEFF") {
		// Windows PowerShell 5.1 decodes BOM-less files as ANSI: a UTF-8 script
		// with Chinese comments then fails to parse. Always write the BOM.
		body = "\uFEFF" + body
	}
	if err := os.WriteFile(scriptPath, []byte(body), 0o644); err != nil {
		return store.Manifest{}, err
	}
	manifest := store.Manifest{
		ID:          spec.ID,
		Title:       firstNonEmpty(spec.Title, spec.ID),
		Description: spec.Description,
		Usage:       spec.Usage,
		Kind:        "script",
		Language:    strings.ToLower(spec.Language),
		Command:     interpreter,
		Args:        []string{scriptPath},
		Source:      scriptPath,
	}
	if err := store.Save(root, &manifest); err != nil {
		return store.Manifest{}, err
	}
	_ = store.WriteReadme(root, &manifest)
	return manifest, nil
}

// RunHook observes a run while it happens: the pid once the process exists and
// every output chunk, so a GUI in another process can show live progress.
// Headless callers pass nil.
type RunHook struct {
	Started func(pid int)
	Output  func(stream string, chunk []byte)
}

// Run executes an installed tool and captures bounded stdout/stderr.
func Run(ctx context.Context, root, id string, args []string, cwd string, timeout time.Duration, hook *RunHook) (Output, error) {
	manifest, err := store.Load(root, id)
	if err != nil {
		return Output{}, err
	}
	if manifest == nil {
		return Output{}, fmt.Errorf("tool %q is not installed; install it first", id)
	}
	if !store.IsEnabled(manifest) {
		return Output{}, fmt.Errorf("tool %q is disabled; enable it in the toolbox page first", id)
	}
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	if timeout > 10*time.Minute {
		timeout = 10 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	argv := make([]string, 0, len(manifest.Args)+len(args))
	argv = append(argv, manifest.Args...)
	argv = append(argv, args...)
	cmd := exec.CommandContext(runCtx, manifest.Command, argv...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	stdout := &capWriter{limit: MaxOutput}
	stderr := &capWriter{limit: MaxOutput}
	if hook != nil && hook.Output != nil {
		stream := hook.Output
		stdout.sink = func(p []byte) { stream("stdout", p) }
		stderr.sink = func(p []byte) { stream("stderr", p) }
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		return Output{}, err
	}
	if hook != nil && hook.Started != nil && cmd.Process != nil {
		hook.Started(cmd.Process.Pid)
	}
	runErr := cmd.Wait()
	out := Output{Stdout: stdout.String(), Stderr: stderr.String(), Truncated: stdout.truncated || stderr.truncated}
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			out.ExitCode = exitErr.ExitCode()
		}
		// The context speaks first: a killed child also reports an exit error,
		// and the caller needs "timeout" or "cancelled", not "exit code 1".
		switch {
		case errors.Is(runCtx.Err(), context.DeadlineExceeded):
			return out, fmt.Errorf("timed out after %s: %w", timeout, context.DeadlineExceeded)
		case errors.Is(runCtx.Err(), context.Canceled):
			return out, context.Canceled
		}
		if errors.As(runErr, &exitErr) {
			return out, nil
		}
		return out, runErr
	}
	return out, nil
}

func installFrom(ctx context.Context, root string, tool catalog.Tool, sources []catalog.Source, progress ProgressFunc) (Result, error) {
	binaries := tool.Binaries
	if len(binaries) == 0 {
		binaries = []string{tool.ID}
	}
	wanted := make(map[string]bool, len(binaries))
	for _, name := range binaries {
		wanted[strings.ToLower(name)] = true
	}
	// Work inside the toolbox root, not the OS temp dir: a hosted process (a
	// sandboxed agent shell, a plugin-spawned child) may not be allowed to
	// create directories under %TEMP%, and staying on the same volume also makes
	// the final move cheap.
	workRoot := filepath.Join(root, ".tmp")
	if err := os.MkdirAll(workRoot, 0o755); err != nil {
		return Result{}, err
	}
	work, err := os.MkdirTemp(workRoot, "work-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(work)

	result := Result{}
	if len(sources) > 1 {
		// Measure the mirrors before committing: a slow first source can cost
		// hours on a large archive, while 512KB per source tells us enough.
		if progress != nil {
			progress("measure", 0, 0)
		}
		speeds := ProbeSources(ctx, sources)
		sources = OrderBySpeed(sources, speeds)
	}
	for index, source := range sources {
		result.Attempts = index + 1
		manifest, err := attempt(ctx, root, tool, source, binaries, wanted, filepath.Join(work, fmt.Sprintf("a%d", index)), progress)
		if err == nil {
			result.Manifest = manifest
			return result, nil
		}
		result.Failures = append(result.Failures, fmt.Sprintf("%s -> %v", source.URL, err))
	}
	return result, fmt.Errorf("all %d source(s) failed for %s:\n%s", len(sources), tool.ID, strings.Join(result.Failures, "\n"))
}

func attempt(ctx context.Context, root string, tool catalog.Tool, source catalog.Source, binaries []string, wanted map[string]bool, work string, progress ProgressFunc) (store.Manifest, error) {
	if err := os.MkdirAll(work, 0o755); err != nil {
		return store.Manifest{}, err
	}
	// Downloads live in a stable directory, not in the per-attempt work dir: a
	// cancelled download must keep its `.part` file so the next attempt can
	// resume with a Range request instead of restarting a slow source.
	downloadDir := filepath.Join(root, ".downloads")
	if err := os.MkdirAll(downloadDir, 0o755); err != nil {
		return store.Manifest{}, err
	}
	downloadPath := filepath.Join(downloadDir, tool.ID+"-"+fileNameFromURL(source.URL))
	size, err := download(ctx, source.URL, downloadPath, progress)
	if err != nil {
		return store.Manifest{}, err
	}
	defer os.Remove(downloadPath)
	if source.SHA256 != "" {
		if err := verifySHA256(downloadPath, source.SHA256); err != nil {
			// Drop the bad file: keeping it would make the retry resume from it.
			_ = os.Remove(downloadPath)
			return store.Manifest{}, err
		}
	}
	if progress != nil {
		progress("extract", 0, 0)
	}
	binDir := store.BinDir(root, tool.ID)
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return store.Manifest{}, err
	}

	found := make(map[string]string)
	if source.Single || tool.Single || isBareFile(downloadPath, source.Archive) {
		target := filepath.Join(binDir, exeName(binaries[0]))
		if err := copyFile(downloadPath, target); err != nil {
			return store.Manifest{}, err
		}
		found[binaries[0]] = target
	} else {
		extracted := filepath.Join(work, "x")
		if err := extract(downloadPath, source.Archive, extracted); err != nil {
			return store.Manifest{}, err
		}
		if err := collect(extracted, binDir, wanted, found); err != nil {
			return store.Manifest{}, err
		}
	}
	if len(found) == 0 {
		return store.Manifest{}, fmt.Errorf("archive contained none of %s", strings.Join(binaries, ", "))
	}
	primary := found[binaries[0]]
	if primary == "" {
		for _, path := range found {
			primary = path
			break
		}
	}
	names := make([]string, 0, len(found))
	for _, name := range binaries {
		if _, ok := found[name]; ok {
			names = append(names, name)
		}
	}
	if progress != nil {
		progress("probe", 0, 0)
	}
	manifest := store.Manifest{
		ID:          tool.ID,
		Title:       firstNonEmpty(tool.Title, tool.ID),
		Description: tool.Description,
		Usage:       tool.Usage,
		Kind:        "binary",
		Binaries:    names,
		Command:     primary,
		Version:     probeVersion(ctx, primary),
		Source:      source.URL,
		SizeBytes:   size,
	}
	if err := store.Save(root, &manifest); err != nil {
		return store.Manifest{}, err
	}
	_ = store.WriteReadme(root, &manifest)
	return manifest, nil
}

// ProgressFunc reports one phase of an install: "download" carries byte counts,
// "extract"/"probe" carry no counters.
type ProgressFunc func(phase string, received, total int64)

// download fetches url into dest, resuming an interrupted `.part` file when the
// server honours a Range request. A cancelled download keeps its `.part` so a
// retry continues instead of restarting a slow source from zero.
func download(ctx context.Context, url, dest string, progress ProgressFunc) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return 0, err
	}
	partial := dest + ".part"
	var offset int64
	if info, err := os.Stat(partial); err == nil {
		offset = info.Size()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	request.Header.Set("User-Agent", "toolbox/0.1")
	if offset > 0 {
		request.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	client := &http.Client{Timeout: 0}
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusPartialContent:
		// Resuming: keep the bytes we already have.
	case http.StatusOK:
		// The server ignored Range (or we started fresh): restart cleanly.
		offset = 0
	case http.StatusRequestedRangeNotSatisfiable:
		// Our partial is already the whole file.
		if err := os.Rename(partial, dest); err == nil {
			return 0, nil
		}
		return 0, fmt.Errorf("HTTP %d %s", response.StatusCode, response.Status)
	default:
		return 0, fmt.Errorf("HTTP %d %s", response.StatusCode, response.Status)
	}

	total := response.ContentLength
	if total > 0 {
		total += offset
	}
	flags := os.O_CREATE | os.O_WRONLY
	if offset == 0 {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_APPEND
	}
	file, err := os.OpenFile(partial, flags, 0o644)
	if err != nil {
		return 0, err
	}
	received := offset
	if progress != nil {
		progress("download", received, total)
	}
	buffer := make([]byte, 256*1024)
	for {
		n, readErr := response.Body.Read(buffer)
		if n > 0 {
			if _, writeErr := file.Write(buffer[:n]); writeErr != nil {
				file.Close()
				return received, writeErr
			}
			received += int64(n)
			if progress != nil {
				progress("download", received, total)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			file.Close()
			if ctx.Err() != nil {
				return received, ctx.Err()
			}
			return received, readErr
		}
	}
	if err := file.Close(); err != nil {
		return received, err
	}
	if received == 0 {
		return 0, fmt.Errorf("empty response body")
	}
	if err := os.Rename(partial, dest); err != nil {
		return received, err
	}
	return received, nil
}

func extract(archivePath, format, destDir string) error {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	kind := strings.ToLower(strings.TrimSpace(format))
	if kind == "" {
		kind = strings.ToLower(archivePath)
	}
	switch {
	case strings.HasSuffix(kind, ".zip") || kind == "zip":
		return extractZip(archivePath, destDir)
	case strings.Contains(kind, "tar.gz") || strings.Contains(kind, "tgz") || strings.HasSuffix(kind, ".tar.gz"):
		return extractTarGz(archivePath, destDir)
	case strings.Contains(kind, "tar.xz") || strings.HasSuffix(kind, ".tar.xz") || strings.HasSuffix(kind, ".xz"):
		return fmt.Errorf("tar.xz is not supported; use a .zip or .tar.gz source")
	default:
		return fmt.Errorf("unsupported archive format %q", kind)
	}
}

func extractZip(archivePath, destDir string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer reader.Close()
	for _, entry := range reader.File {
		name := filepath.Base(entry.Name)
		if name == "" || name == "." || name == "/" || entry.FileInfo().IsDir() {
			continue
		}
		target := filepath.Join(destDir, name)
		if err := copyZipEntry(entry, target); err != nil {
			return err
		}
	}
	return nil
}

func copyZipEntry(entry *zip.File, target string) error {
	reader, err := entry.Open()
	if err != nil {
		return err
	}
	defer reader.Close()
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	writer, err := os.Create(target)
	if err != nil {
		return err
	}
	defer writer.Close()
	_, err = io.Copy(writer, reader)
	return err
}

func extractTarGz(archivePath, destDir string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		name := filepath.Base(header.Name)
		if name == "" || name == "." {
			continue
		}
		target := filepath.Join(destDir, name)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		writer, err := os.Create(target)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(writer, reader)
		writer.Close()
		if copyErr != nil {
			return copyErr
		}
	}
}

func collect(dir, binDir string, wanted map[string]bool, found map[string]string) error {
	return filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		name := strings.ToLower(filepath.Base(path))
		name = strings.TrimSuffix(name, ".exe")
		if !wanted[name] {
			return nil
		}
		if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(path), ".exe") {
			return nil
		}
		target := filepath.Join(binDir, exeName(filepath.Base(path)))
		if err := copyFile(path, target); err != nil {
			return err
		}
		for key := range wanted {
			if key == name {
				if _, ok := found[key]; !ok {
					found[key] = target
				}
			}
		}
		return nil
	})
}

func probeVersion(ctx context.Context, command string) string {
	for _, flag := range []string{"--version", "-version", "-V"} {
		probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		out, err := exec.CommandContext(probeCtx, command, flag).CombinedOutput()
		cancel()
		if err != nil {
			continue
		}
		line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
		// Some tools treat an unknown flag as an argument and print data instead
		// of a version (dust renders a tree for -version). Reject that shape.
		if strings.ContainsAny(line, "│├└─┌┴┬┐┘") || len(line) > 80 {
			continue
		}
		if line != "" {
			if len(line) > 120 {
				line = line[:120]
			}
			return line
		}
	}
	return ""
}

func copyFile(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	out, err := os.Create(target)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return os.Chmod(target, 0o755)
}

func fileNameFromURL(url string) string {
	trimmed := url
	if index := strings.IndexAny(trimmed, "?#"); index >= 0 {
		trimmed = trimmed[:index]
	}
	trimmed = strings.TrimRight(trimmed, "/")
	name := filepath.Base(trimmed)
	if name == "" || name == "." || name == "/" {
		return "download.bin"
	}
	return name
}

// isBareFile reports whether the download is an executable rather than an
// archive. Catalog entries write the archive kind as "zip" / "tar.gz" (no dot),
// so match the words, not the extensions — matching only ".zip" made every
// archive look bare and copied the whole download in as the binary.
func isBareFile(path, format string) bool {
	kind := strings.ToLower(strings.TrimSpace(firstNonEmpty(format, path)))
	if kind == "" {
		return true
	}
	for _, marker := range []string{"zip", "tar", "tgz", "xz", "gzip", "7z"} {
		if strings.Contains(kind, marker) {
			return false
		}
	}
	return true
}

func exeName(name string) string {
	if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(name), ".exe") {
		return name + ".exe"
	}
	return name
}

type capWriter struct {
	builder   strings.Builder
	limit     int
	truncated bool
	// sink mirrors every chunk before the cap is applied, so a live console
	// keeps showing progress after the returned result has been cut off.
	sink func(p []byte)
}

func (w *capWriter) Write(data []byte) (int, error) {
	if w.sink != nil {
		w.sink(data)
	}
	remaining := w.limit - w.builder.Len()
	if remaining <= 0 {
		w.truncated = true
		return len(data), nil
	}
	if len(data) > remaining {
		w.builder.Write(data[:remaining])
		w.truncated = true
		return len(data), nil
	}
	w.builder.Write(data)
	return len(data), nil
}

func (w *capWriter) String() string { return w.builder.String() }

// verifySHA256 rejects a download whose bytes do not match the catalog digest,
// before anything is extracted or executed.
func verifySHA256(path, want string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	got := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(got, strings.TrimSpace(want)) {
		return fmt.Errorf("sha256 校验失败：期望 %s，实际 %s（已删除下载文件，未执行任何东西）", strings.TrimSpace(want), got)
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// findBundledPython looks for the Python runtime DSH ships under DSH_HOME, so an
// archived script can run on a machine whose PATH has no python.
func findBundledPython() string {
	home := os.Getenv("DSH_HOME")
	if home == "" {
		if user, err := os.UserHomeDir(); err == nil {
			home = filepath.Join(user, ".dsh")
		}
	}
	if home == "" {
		return "python"
	}
	runtimes := filepath.Join(home, "dsh-runtimes")
	entries, err := os.ReadDir(runtimes)
	if err != nil {
		return "python"
	}
	for _, entry := range entries {
		candidate := filepath.Join(runtimes, entry.Name(), "dependencies", "python", "python.exe")
		if _, statErr := os.Stat(candidate); statErr == nil {
			return candidate
		}
	}
	return "python"
}
