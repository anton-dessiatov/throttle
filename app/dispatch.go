package app

import "log"

type dispatchTunnel struct {
	tunnel     tunnel
	lastConfig TunnelConfig
}

func dispatch(configUpdate <-chan Configuration, gs *gracefulShutdown) {
	gs.waitGroup.Add(1)
	defer gs.waitGroup.Done()

	tunnels := make(map[ListenAt]*dispatchTunnel)

	for {
		select {
		case config := <-configUpdate:
			log.Printf("Configuration update: %v", config)
			// Sweep the map and update configuration for tunnels that need it
			for k, v := range config.Tunnels {
				t, ok := tunnels[k]
				if ok {
					if t.lastConfig != v {
						t.tunnel.configUpdate <- v
						t.lastConfig = v
					}
				} else {
					t, err := CreateTunnel(k, v, gs)
					if err != nil {
						log.Printf("Failed to create tunnel for %q: %v", k, err)
					} else {
						tunnels[k] = &dispatchTunnel{
							tunnel:     t,
							lastConfig: v,
						}
					}
				}
			}
			// Sweep existing tunnels to shutdown ones that are no longer present in
			// configuration:
			survivors := make(map[ListenAt]*dispatchTunnel)
			for k, v := range tunnels {
				_, ok := config.Tunnels[k]
				if ok {
					survivors[k] = v
				} else {
					close(v.tunnel.shutdown)
				}
			}

			tunnels = survivors
		case <-gs.quit:
			// There's no need to shut down individual tunnels because they will react
			// to the very same gs.quit close
			return
		}
	}
}
