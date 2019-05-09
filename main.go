package main

import (
	"flag"
	"log"

	"github.com/anton-dessiatov/throttle/app"
	"net/http"
	_ "net/http/pprof"
)

func main() {
	go func() {
		log.Println(http.ListenAndServe("localhost:6060", nil))
	}()
	var configPath string
	flag.StringVar(&configPath, "config", "config.json", "Path to configuration file")
	flag.Parse()

	app.Run(configPath)
}
