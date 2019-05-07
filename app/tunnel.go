package app

import "log"

type tunnel struct {
	configUpdate chan<- TunnelConfig
	shutdown     chan<- struct{}
}

func CreateTunnel(listenAt ListenAt, config TunnelConfig, gs *gracefulShutdown) (tunnel, error) {
	shutdown := make(chan struct{})
	configUpdate := make(chan TunnelConfig)

	log.Printf("Starting tunnel at %q", listenAt)

	gs.waitGroup.Add(1)
	go func() {
		defer gs.waitGroup.Done()
		for {
			select {
			case config := <-configUpdate:
				log.Printf("Tunnel at %q config updated: %v", listenAt, config)
			case <-shutdown:
				log.Printf("Tunnel at %q shutting down by a signal from dispatch", listenAt)
				return
			case <-gs.quit:
				log.Printf("Tunnel at %q shutting down by a global graceful shutdown signal", listenAt)
				return
			}
		}
	}()

	result := tunnel{
		configUpdate: configUpdate,
		shutdown:     shutdown,
	}
	return result, nil
}
