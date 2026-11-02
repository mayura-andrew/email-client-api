// Command devserver runs the desktop app's handler over plain HTTP so the UI
// can be developed in a browser: `bun run dev` in web/ proxies /api here.
package main

import (
	"flag"
	"log"
	"net/http"
	"os"

	"github.com/mayura-andrew/email-client/assests"
	"github.com/mayura-andrew/email-client/internal/desktop"
	"github.com/mayura-andrew/email-client/internal/jsonlog"
	"github.com/mayura-andrew/email-client/migrations"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:4000", "listen address (keep it on localhost: setup is unauthenticated)")
	dir := flag.String("config-dir", ".devdata", "where config.json is kept")
	ui := flag.String("ui", "web/out", "exported UI to serve (run `bun run build` in web/)")
	flag.Parse()

	logger := jsonlog.New(os.Stdout, jsonlog.LevelInfo)
	host, err := desktop.New(*dir, os.DirFS(*ui), migrations.FS, assests.Template, logger)
	if err != nil {
		log.Fatal(err)
	}
	host.Start()
	defer host.Stop()
	logger.PrintInfo("listening", map[string]string{"addr": "http://" + *addr})
	log.Fatal(http.ListenAndServe(*addr, host))
}
