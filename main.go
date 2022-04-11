package main

import (
	"context"
	"embed"
	"encoding/base64"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"

	"github.com/BurntSushi/toml"
	"github.com/fsnotify/fsnotify"
	"github.com/go-chi/chi/v5"
	"github.com/gohugoio/hugo/livereload"
	"go.uber.org/zap"
)

// ErrHandlerFunc is a handler func that fails
type ErrHandlerFunc = func(w http.ResponseWriter, r *http.Request) error

// Dir configures a single watched directory
type Dir struct {
	Path                  string   `toml:"path"`
	TemplateFilePattern   string   `toml:"template_file_pattern"`
	TestFilePattern       string   `toml:"test_file_pattern"`
	ScreenshotFilePattern string   `toml:"screenshot_file_pattern"`
	TestCommand           []string `toml:"test_command"`
}

// Config configures rir
type Config struct {
	ListenAddr string `toml:"listen_addr"`
	Dirs       []Dir  `toml:"dir"`
}

// retest will run tests and visual diffing
func retest(logs *zap.Logger, wdir Dir) error {
	logs.Info("retesting directory", zap.String("dir", wdir.Path))

	cmd := exec.Command(wdir.TestCommand[0], wdir.TestCommand[1:]...)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to run test command: %w", err)
	}

	return nil
}

// Do runs 'f' async and logs any error
func do(logs *zap.Logger, m string, f func() error) {
	go func() {
		if err := f(); err != nil {
			logs.Error(m, zap.Error(err))
		}
	}()
}

// reload will reload any open browsers windows that have screenshots open
func reload(logs *zap.Logger, wdir Dir) error {
	logs.Info("reloading screenshots", zap.String("dir", wdir.Path))
	livereload.ForceRefresh()
	return nil
}

// init the pubsub hub for live reloading
func init() {
	livereload.Initialize()
}

// watch handles filesystem notifacations
func watch(logs *zap.Logger, cfg Config, w *fsnotify.Watcher) {
	for {
		select {
		case ev, ok := <-w.Events:
			if !ok {
				logs.Error("failed to read next event, stopping")
				return
			}

			if ev.Op == fsnotify.Chmod {
				continue // ignore
			}

			// concurrently handle dir refreshes
			for _, wdir := range cfg.Dirs {
				if m, _ := filepath.Match(filepath.Join(wdir.Path, wdir.TestFilePattern), ev.Name); m {
					go do(logs, "failed to test", func() error { return retest(logs, wdir) })
				} else if m, _ := filepath.Match(filepath.Join(wdir.Path, wdir.TemplateFilePattern), ev.Name); m {
					go do(logs, "failed to test", func() error { return retest(logs, wdir) })
				} else if m, _ := filepath.Match(filepath.Join(wdir.Path, wdir.ScreenshotFilePattern), ev.Name); m {
					go do(logs, "failed to reload", func() error { return reload(logs, wdir) })
				}
			}

			logs.Debug("fsnotify event", zap.Any("ev", ev))
		case err, ok := <-w.Errors:
			if !ok {
				logs.Error("watcher error, stopping")
				return
			}
			logs.Error("fsnotify error", zap.Error(err))
		}
	}
}

// errh turns a err handlerfunc in a regular handlerfunc
func errh(h ErrHandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

// index shows all screenshots as configured per dir
func index(cfg Config, v *template.Template) ErrHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		type screeninfo struct {
			B64  string
			Name string
		}
		var data struct {
			Config      Config
			Screenshots map[int][]screeninfo
		}

		data.Config = cfg
		data.Screenshots = map[int][]screeninfo{}
		for i, wdir := range cfg.Dirs {
			matches, err := filepath.Glob(filepath.Join(wdir.Path, wdir.ScreenshotFilePattern))
			if err != nil {
				return fmt.Errorf("failed to glob Screenshots: %w", err)
			}

			if len(matches) < 1 {
				continue
			}

			for _, match := range matches {
				rel, _ := filepath.Rel(wdir.Path, match)
				data.Screenshots[i] = append(data.Screenshots[i], screeninfo{
					B64:  base64.URLEncoding.EncodeToString([]byte(rel)),
					Name: rel,
				})
			}
		}

		return v.ExecuteTemplate(w, "index.gotmpl", data)
	}
}

// screenshot renders the screenshot view
func view(cfg Config, v *template.Template) ErrHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		dirIdx := chi.URLParam(r, "dirIdx")
		screenshotNameB64 := chi.URLParam(r, "screenshot")
		screenshotName, err := base64.URLEncoding.DecodeString(screenshotNameB64)
		if err != nil {
			return fmt.Errorf("failed to de decode base64 encoded screenshot name: %w", err)
		}

		return v.ExecuteTemplate(w, "screenshot.gotmpl", struct {
			Name    string
			B64Name string
			DirIdx  string
		}{string(screenshotName), screenshotNameB64, dirIdx})
	}
}

// screenshot renders the screenshot image
func screenshot(cfg Config, v *template.Template) ErrHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		screenshotNameB64 := chi.URLParam(r, "screenshot")
		screenshotName, err := base64.URLEncoding.DecodeString(screenshotNameB64)
		if err != nil {
			return fmt.Errorf("failed to de decode base64 encoded screenshot name: %w", err)
		}

		dirIdx, err := strconv.Atoi(chi.URLParam(r, "dirIdx"))
		if err != nil {
			return fmt.Errorf("failed to decode dir idx: %w", err)
		}

		http.ServeFile(w, r, filepath.Join(cfg.Dirs[dirIdx].Path, string(screenshotName)))
		return nil
	}
}

//go:embed *.gotmpl
var tmpls embed.FS

// run sets up the filesystem watcher
func run(ctx context.Context, logs *zap.Logger) error {
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt)
	defer cancel()

	// parse config
	var cfg Config
	if _, err := toml.DecodeFS(os.DirFS("."), "rir.toml", &cfg); err != nil {
		return fmt.Errorf("failed to decode config file: %w", err)
	}

	// parse templates for web interface
	v, err := template.ParseFS(tmpls, "*.gotmpl")
	if err != nil {
		return fmt.Errorf("failed to parse templates: %w", err)
	}

	// serve web interface
	r := chi.NewRouter()
	r.Method("GET", "/", errh(index(cfg, v)))
	r.Method("GET", "/view/{dirIdx}/{screenshot}", errh(view(cfg, v)))
	r.Method("GET", "/screenshot/{dirIdx}/{screenshot}", errh(screenshot(cfg, v)))
	r.Mount("/livereload", http.HandlerFunc(livereload.Handler))
	r.Mount("/livereload.js", http.HandlerFunc(livereload.ServeJS))
	go http.ListenAndServe(cfg.ListenAddr, r)

	// setup watcher
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to init file watcher: %w", err)
	}
	defer w.Close()

	// add watches
	go watch(logs, cfg, w)
	for _, wdir := range cfg.Dirs {
		if err := w.Add(wdir.Path); err != nil {
			return fmt.Errorf("failed to add watch for dir '%s' %w", wdir.Path, err)
		}

		logs.Info("added watch for dir", zap.String("path", wdir.Path))
	}

	// block until done
	logs.Info("running, Ctrl+c to exit", zap.String("listen_addr", cfg.ListenAddr))
	select {
	case <-ctx.Done():
		logs.Info("shutting down")
		return nil
	}
}

// main entrypoint
func main() {
	ctx := context.Background()
	logs, _ := zap.NewDevelopment()
	if err := run(ctx, logs.Named("run")); err != nil {
		panic(err)
	}
}
