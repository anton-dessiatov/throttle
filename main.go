package main

import (
	"flag"

	"github.com/anton-dessiatov/throttle/app"
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "config.json", "Path to configuration file")
	flag.Parse()

	app.Run(configPath)
}
