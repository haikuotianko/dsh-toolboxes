// Package mcpserver exposes the toolbox over MCP: list, install, run, remove and
// archive-script tools, plus the same operations as methods the HTTP API reuses.
package mcpserver

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/haikuotianko/dsh-toolboxes/internal/catalog"
	"github.com/haikuotianko/dsh-toolboxes/internal/install"
	"github.com/haikuotianko/dsh-toolboxes/internal/store"
)

// Service owns the toolbox root.
type Service struct {
	root        string
	logf        func(format string, args ...any)
	jobRegistry *jobs
}

// New creates the service; logf may be nil.
func New(root string, logf func(string, ...any)) *Service {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Service{root: root, logf: logf, jobRegistry: newJobs()}
}

// Root returns the toolbox directory.
func (s *Service) Root() string { return s.root }

// SourceView is one download origin shown to a caller.
type SourceView struct {
	URL     string `json:"url"`
	Archive string `json:"archive,omitempty"`
}

// ToolView is one row of state.
type ToolView struct {
	ID          string       `json:"id"`
	Title       string       `json:"title,omitempty"`
	Category    string       `json:"category,omitempty"`
	Description string       `json:"description,omitempty"`
	Usage       string       `json:"usage,omitempty"`
	Kind        string       `json:"kind,omitempty"`
	Version     string       `json:"version,omitempty"`
	Installed   bool         `json:"installed"`
	Enabled     bool         `json:"enabled"`
	PlatformMissing bool    `json:"platformMissing,omitempty"`
	Sources     []SourceView `json:"sources,omitempty"`
}

// StateOutput is the whole toolbox state.
type StateOutput struct {
	Root      string     `json:"root"`
	Platform  string     `json:"platform"`
	Installed []ToolView `json:"installed"`
	Available []ToolView `json:"available"`
}

// State reports installed tools and installable catalog entries for this OS.
func (s *Service) State() (StateOutput, error) {
	out := StateOutput{Root: s.root, Platform: runtime.GOOS + "-" + runtime.GOARCH}
	manifests, err := store.List(s.root)
	if err != nil {
		return out, err
	}
	installed := make(map[string]bool, len(manifests))
	for _, manifest := range manifests {
		installed[manifest.ID] = true
		out.Installed = append(out.Installed, ToolView{
			ID:          manifest.ID,
			Title:       manifest.Title,
			Description: manifest.Description,
			Usage:       manifest.Usage,
			Kind:        manifest.Kind,
			Version:     manifest.Version,
			Installed:   true,
			Enabled:     store.IsEnabled(manifest),
		})
	}
	if out.Installed == nil {
		out.Installed = []ToolView{}
	}
	cat, err := catalog.Load(s.root)
	if err != nil {
		return out, err
	}
	for _, tool := range cat.Tools {
		if installed[tool.ID] {
			continue
		}
		// Keep the entry visible even when every source targets another OS: the
		// page then asks the user for a URL instead of hiding the tool.
		platformMissing := len(tool.Sources) > 0 && len(tool.SourcesFor(runtime.GOOS, runtime.GOARCH)) == 0
		view := ToolView{
			ID:          tool.ID,
			Title:       tool.Title,
			Category:    tool.Category,
			Description: tool.Description,
			Usage:       tool.Usage,
			Installed:   false,
			PlatformMissing: platformMissing,
		}
		for _, source := range tool.SourcesFor(runtime.GOOS, runtime.GOARCH) {
			view.Sources = append(view.Sources, SourceView{URL: source.URL, Archive: source.Archive})
		}
		out.Available = append(out.Available, view)
	}
	if out.Available == nil {
		out.Available = []ToolView{}
	}
	return out, nil
}

// InstallOutput is the result of an install.
type InstallOutput struct {
	Tool     string `json:"tool"`
	Version  string `json:"version,omitempty"`
	Binary   string `json:"binary"`
	Source   string `json:"source,omitempty"`
	Attempts int    `json:"attempts"`
}

// Install installs a catalog entry, or a custom URL when url is set.
func (s *Service) Install(ctx context.Context, tool, url, archive string) (InstallOutput, error) {
	tool = strings.TrimSpace(tool)
	var (
		result install.Result
		err    error
	)
	if url != "" {
		result, err = install.InstallCustom(ctx, s.root, tool, url, archive)
	} else {
		cat, loadErr := catalog.Load(s.root)
		if loadErr != nil {
			return InstallOutput{}, loadErr
		}
		entry, ok := cat.Find(tool)
		if !ok {
			return InstallOutput{}, fmt.Errorf("未知工具 %q；toolbox_list 可以看可用清单", tool)
		}
		result, err = install.InstallTool(ctx, s.root, entry)
	}
	if err != nil {
		return InstallOutput{Attempts: result.Attempts}, err
	}
	m := result.Manifest
	return InstallOutput{Tool: m.ID, Version: m.Version, Binary: m.Command, Source: m.Source, Attempts: result.Attempts}, nil
}

// ImportPath registers a local executable the user picked.
func (s *Service) ImportPath(ctx context.Context, tool, path string) (InstallOutput, error) {
	result, err := install.ImportFile(ctx, s.root, tool, path)
	if err != nil {
		return InstallOutput{}, err
	}
	m := result.Manifest
	return InstallOutput{Tool: m.ID, Version: m.Version, Binary: m.Command, Source: m.Source, Attempts: 1}, nil
}

// RunOutput is one captured run.
type RunOutput struct {
	Tool      string `json:"tool"`
	ExitCode  int    `json:"exitCode"`
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	Truncated bool   `json:"truncated"`
}

// RemoveOutput reports a deletion.
type RemoveOutput struct {
	Removed string `json:"removed"`
}

// Remove deletes an installed tool.
func (s *Service) Remove(tool string) (RemoveOutput, error) {
	if err := store.Remove(s.root, strings.TrimSpace(tool)); err != nil {
		return RemoveOutput{}, err
	}
	return RemoveOutput{Removed: tool}, nil
}

// SaveScriptOutput reports an archived script.
type SaveScriptOutput struct {
	Archived    string `json:"archived"`
	Script      string `json:"script"`
	Interpreter string `json:"interpreter"`
}

// SaveScript archives a reusable script.
func (s *Service) SaveScript(spec install.ScriptSpec) (SaveScriptOutput, error) {
	manifest, err := install.SaveScript(s.root, spec)
	if err != nil {
		return SaveScriptOutput{}, err
	}
	return SaveScriptOutput{Archived: manifest.ID, Script: manifest.Source, Interpreter: manifest.Command}, nil
}

// RuntimesOutput lists the languages this machine can run.
type RuntimesOutput struct {
	Runtimes []RuntimeView `json:"runtimes"`
}

// ---- MCP tool inputs ----

type listInput struct{}

type installInput struct {
	Tool    string `json:"tool" jsonschema:"catalog id, e.g. ffmpeg / rg / fd / jq / yt-dlp, or a new id when url is given"`
	URL     string `json:"url,omitempty" jsonschema:"optional custom download URL; a single-file binary or a .zip/.tar.gz archive"`
	Archive string `json:"archive,omitempty" jsonschema:"archive kind for a custom url: zip or tar.gz; omit for a bare executable"`
}

type runInput struct {
	Tool      string   `json:"tool" jsonschema:"installed tool id"`
	Args      []string `json:"args,omitempty" jsonschema:"CLI arguments, e.g. [\"-i\",\"in.mp4\",\"out.webm\"]"`
	Cwd       string   `json:"cwd,omitempty" jsonschema:"working directory for relative paths"`
	TimeoutMs int      `json:"timeout_ms,omitempty" jsonschema:"wall-clock limit in ms, default 120000, max 600000"`
}

type removeInput struct {
	Tool string `json:"tool" jsonschema:"installed tool id"`
}

type saveScriptInput struct {
	Name        string `json:"name" jsonschema:"tool id, lowercase with dashes, e.g. pdf-split"`
	Title       string `json:"title,omitempty" jsonschema:"short display title"`
	Description string `json:"description" jsonschema:"what the script does, in the user's language"`
	Usage       string `json:"usage" jsonschema:"how to call it through toolbox_run, with a concrete example"`
	Language    string `json:"language" jsonschema:"python | node | powershell"`
	Content     string `json:"content" jsonschema:"full script source"`
	Command     string `json:"command,omitempty" jsonschema:"optional absolute interpreter path"`
}

// Version is stamped by main so the MCP handshake reports the release version
// instead of a stale literal.
var Version = "dev"

// NewServer builds one MCP server instance with every toolbox tool.
func (s *Service) NewServer() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "toolbox", Version: Version}, &mcp.ServerOptions{
		Instructions: "工具箱：先 toolbox_list 看已装工具与可装清单；ffmpeg/rg/fd/jq/yt-dlp 这类通用活优先调用已装工具，不要临时写脚本；确实需要新脚本且它不只服务当前项目时，用 toolbox_save_script 归档。",
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "toolbox_list",
		Description: "List installed toolbox CLIs with their one-line usage, plus catalog entries installable on this OS with their download sources. Check here before writing a throwaway script, and for requests such as listing a directory tree or finding which folder uses the most space (dust), counting lines of code (tokei), searching content or filenames (rg/fd), editing JSON/YAML (jq/yq), debugging HTTP (curl/xh), or converting media (ffmpeg). If the best tool is not installed yet, ask the user before installing it.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in listInput) (*mcp.CallToolResult, StateOutput, error) {
		state, err := s.State()
		return nil, state, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "toolbox_install",
		Description: "Install a toolbox CLI for this OS. Tries every built-in download source in order and falls through to the next on failure; pass url to install from a custom link instead. Use this instead of downloading and unpacking archives by hand.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in installInput) (*mcp.CallToolResult, InstallOutput, error) {
		out, err := s.mustInstall(ctx, in)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "toolbox_run",
		Description: "Run an installed toolbox CLI and return its exit code with stdout/stderr. This is the normal way to convert video, extract frames, resize images, probe files, download media, or search with rg/fd — prefer it over writing a new script.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in runInput) (*mcp.CallToolResult, RunOutput, error) {
		out, err := s.Run(ctx, in.Tool, in.Args, in.Cwd, in.TimeoutMs, OriginAgent)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "toolbox_remove",
		Description: "Uninstall a toolbox CLI and delete its files. Only use when the user asks to remove it or it is broken beyond repair.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in removeInput) (*mcp.CallToolResult, RemoveOutput, error) {
		out, err := s.Remove(in.Tool)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "toolbox_save_script",
		Description: "Archive a reusable script into the toolbox with its usage. Check toolbox_runtimes first so the language exists on this machine. Call this when a script you wrote is generic (clear inputs/outputs, no project-private paths or constants, parameterised) and will likely be needed again — it becomes callable through toolbox_run afterwards. Tell the user what you archived.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in saveScriptInput) (*mcp.CallToolResult, SaveScriptOutput, error) {
		out, err := s.SaveScript(install.ScriptSpec{
			ID:          in.Name,
			Title:       in.Title,
			Description: in.Description,
			Usage:       in.Usage,
			Language:    in.Language,
			Content:     in.Content,
			Command:     in.Command,
		})
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "toolbox_runtimes",
		Description: "List the programming runtimes and compilers this machine can actually execute (node, python, go, rustc, powershell, …) with their versions. Call this before writing any script so the language you pick exists here — dense computation favors Go/Rust, text and data work favors Python, Windows system tasks favor PowerShell 5.1.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, RuntimesOutput, error) {
		return nil, RuntimesOutput{Runtimes: Runtimes()}, nil
	})

	return server
}

// mustInstall runs one install to completion for the MCP caller: the page polls
// the job, the agent waits for the final result.
func (s *Service) mustInstall(ctx context.Context, in installInput) (InstallOutput, error) {
	job, err := s.StartInstall(in.Tool, in.URL, in.Archive)
	if err != nil {
		return InstallOutput{}, err
	}
	view, err := s.WaitInstall(ctx, job)
	if err != nil {
		return InstallOutput{Tool: in.Tool, Attempts: view.Attempts}, err
	}
	if view.Result == nil {
		return InstallOutput{Tool: in.Tool}, fmt.Errorf("install finished without a result")
	}
	s.logf("installed %s from %s", view.Result.Tool, view.Result.Source)
	return *view.Result, nil
}
