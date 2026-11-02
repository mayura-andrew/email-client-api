// Command email-client is the AI4ALL Certificate Mailer desktop app (Wails).
// The window's requests are handled in-process by desktop.Host: the exported
// UI, first-run setup, and the API with its delivery workers.
package main

import (
	"context"
	"embed"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"

	"github.com/mayura-andrew/email-client/assests"
	"github.com/mayura-andrew/email-client/internal/desktop"
	"github.com/mayura-andrew/email-client/internal/jsonlog"
	"github.com/mayura-andrew/email-client/migrations"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed all:web/out
var ui embed.FS

const maxLogBytes = 5 << 20

func main() {
	dir, err := desktop.ConfigDir()
	if err != nil {
		log.Fatal(err)
	}
	logger := jsonlog.New(openLog(dir), jsonlog.LevelInfo)

	assets, err := fs.Sub(ui, "web/out")
	if err != nil {
		log.Fatal(err)
	}
	host, err := desktop.New(dir, assets, migrations.FS, assests.Template, logger)
	if err != nil {
		logger.PrintFatal(err, map[string]string{"component": "config"})
	}

	err = wails.Run(&options.App{
		Title:            "AI4ALL Certificate Mailer",
		Width:            1360,
		Height:           880,
		MinWidth:         1024,
		MinHeight:        680,
		BackgroundColour: &options.RGBA{R: 244, G: 246, B: 245, A: 255},
		// Assets is nil, so every request, including the UI files, goes to host.
		AssetServer: &assetserver.Options{Handler: host},
		// Two copies would run two sets of workers against one config.
		SingleInstanceLock: &options.SingleInstanceLock{UniqueId: "org.ai4all.certificate-mailer"},
		OnStartup:          func(context.Context) { host.Start() },
		OnBeforeClose: func(ctx context.Context) bool {
			if !host.Sending(ctx) {
				return false
			}
			answer, err := runtime.MessageDialog(ctx, runtime.MessageDialogOptions{
				Type:    runtime.QuestionDialog,
				Title:   "Certificates are still being sent",
				Message: "Closing the app pauses sending. It continues the next time you open the app.\n\nClose anyway?",
			})
			return err == nil && answer != "Yes"
		},
		OnShutdown: func(context.Context) { host.Stop() },
		Windows:    &windows.Options{WebviewUserDataPath: filepath.Join(dir, "webview")},
	})
	if err != nil {
		logger.PrintFatal(err, nil)
	}
}

// openLog appends to logs/app.log in the config directory, starting over once
// it passes 5 MB, and mirrors to stdout for `wails dev`.
func openLog(dir string) io.Writer {
	logs := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logs, 0o700); err != nil {
		return os.Stdout
	}
	path := filepath.Join(logs, "app.log")
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if info, err := os.Stat(path); err == nil && info.Size() > maxLogBytes {
		flags |= os.O_TRUNC
	}
	file, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		return os.Stdout
	}
	return io.MultiWriter(file, os.Stdout)
}
