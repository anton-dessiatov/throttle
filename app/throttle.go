package app

import (
	"log"
	"os"
	"os/signal"
	"sync"
)

// Everything needed by components for a graceful shutdown (e.g. to stop
// goroutines running in the background)
type gracefulShutdown struct {
	quit      <-chan struct{}
	waitGroup *sync.WaitGroup
}

// Run gets the party started
func Run() {
	log.Println("Running")

	quit := make(chan struct{})
	gs := &gracefulShutdown{
		quit:      quit,
		waitGroup: new(sync.WaitGroup),
	}

	configUpdate := make(chan ConfigurationJSON)

	go dispatch(configUpdate, gs)

	const configPath = "config.json"
	err := LoadAndWatch(configPath, configUpdate, gs)
	if err != nil {
		log.Fatalf("Failed to load config file at %q: %v", configPath, err)
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)

	<-c
	close(quit)
	log.Println("Signalled graceful shutdown")
	gs.waitGroup.Wait()
	log.Println("Completed graceful shutdown")
}
