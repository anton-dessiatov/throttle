package app

import (
	"log"
	"sync"
)

type dispatchTunnel struct {
	tunnel     Tunnel
	lastLimits TunnelLimits
	waitGroup  *sync.WaitGroup
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
					close(v.tunnel.Shutdown)
					v.waitGroup.Wait()
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
						t.tunnel.LimitsUpdate <- tunnelLimits
						t.lastLimits = tunnelLimits
					}
				} else {
					wg := new(sync.WaitGroup)
					t, err := CreateTunnel(tunnelKey.listenAt, tunnelKey.connectTo, tunnelLimits, wg)
					if err != nil {
						log.Printf("Failed to create tunnel for %q: %v", tunnelKey, err)
					} else {
						tunnels[tunnelKey] = &dispatchTunnel{
							tunnel:     t,
							lastLimits: tunnelLimits,
							waitGroup:  wg,
						}
					}
				}
			}
		case <-gs.quit:
			for _, v := range tunnels {
				close(v.tunnel.Shutdown)
				v.waitGroup.Wait()
			}
			return
		}
	}
}
