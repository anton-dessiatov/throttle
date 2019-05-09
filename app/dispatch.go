package app

import (
	"log"
)

type dispatchTunnel struct {
	tunnel     *Tunnel
	lastLimits TunnelLimits
}

type tunnelKey struct {
	listenAt  ListenAt
	connectTo ConnectTo
}

func dispatch(configUpdate <-chan ConfigurationJSON, gs *gracefulShutdown) {
	gs.waitGroup.Add(1)
	defer gs.waitGroup.Done()

	tunnels := make(map[tunnelKey]*dispatchTunnel)

	for {
		select {
		case config := <-configUpdate:
			log.Printf("Configuration update: %v", config)
			// Sweep existing tunnels to shutdown ones that are no longer present in
			// configuration:
			survivors := make(map[tunnelKey]*dispatchTunnel)
			for k, v := range tunnels {
				configTunnel, ok := config.Tunnels[k.listenAt]
				if ok && k.connectTo == configTunnel.ConnectTo {
					survivors[k] = v
				} else {
					v.tunnel.Shutdown()
				}
			}

			tunnels = survivors

			// Sweep the map and update configuration for tunnels that need it
			for k, v := range config.Tunnels {
				tunnelKey := tunnelKey{
					listenAt:  k,
					connectTo: v.ConnectTo,
				}
				tunnelLimits := TunnelLimits{
					TunnelLimit:     v.TunnelLimit,
					ConnectionLimit: v.ConnectionLimit,
				}
				t, ok := tunnels[tunnelKey]
				if ok {
					if t.lastLimits != tunnelLimits {
						t.tunnel.UpdateLimits(tunnelLimits)
						t.lastLimits = tunnelLimits
					}
				} else {
					t, err := NewTunnel(tunnelKey.listenAt, tunnelKey.connectTo, tunnelLimits)
					if err != nil {
						log.Printf("Failed to create tunnel for %q: %v", tunnelKey, err)
					} else {
						tunnels[tunnelKey] = &dispatchTunnel{
							tunnel:     t,
							lastLimits: tunnelLimits,
						}
					}
				}
			}
		case <-gs.quit:
			for _, v := range tunnels {
				v.tunnel.Shutdown()
			}
			return
		} // select
	} // for
}
