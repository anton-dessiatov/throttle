package app

import (
	"context"
	"log"
	"net"

	"golang.org/x/time/rate"
)

type TunnelLimits struct {
	TunnelLimit     Limit
	ConnectionLimit Limit
}

// Tunnel is a structure that contains everything you might need to manage an
// existing TCP tunnel
type Tunnel struct {
	// Send configuration on this channel to update the tunnel
	LimitsUpdate chan<- TunnelLimits
	// Close this channel to shut down the tunnel
	Shutdown chan<- struct{}
}

type internalTunnel struct {
	connectTo    ConnectTo
	limitsUpdate chan TunnelLimits
	shutdown     chan struct{}
	listener     net.Listener
	globalQuit   <-chan struct{}
}

func (it internalTunnel) toTunnel() Tunnel {
	return Tunnel{
		LimitsUpdate: it.limitsUpdate,
		Shutdown:     it.shutdown,
	}
}

// CreateTunnel creates a traffic forwarding tunnel with a given listen port
// spec and configuration and returns a structure containing control channels
// for the new tunnel.
func CreateTunnel(listenAt ListenAt, connectTo ConnectTo, limits TunnelLimits, gs *gracefulShutdown) (Tunnel, error) {
	shutdown := make(chan struct{})
	limitsUpdate := make(chan TunnelLimits)

	log.Printf("Starting tunnel at %q", listenAt)

	l, err := net.Listen("tcp", string(listenAt))
	if err != nil {
		log.Printf("Failed to listen at %q: %v", listenAt, err)
		return Tunnel{}, err
	}
	// It's internalTunnel's run() responsibility to close the listener
	result := &internalTunnel{
		connectTo:    connectTo,
		limitsUpdate: limitsUpdate,
		shutdown:     shutdown,
		listener:     l,
		globalQuit:   gs.quit,
	}

	gs.waitGroup.Add(1)
	go func() {
		defer gs.waitGroup.Done()
		for {
			err := result.run(string(listenAt))
			if err == nil {
				return
			}
			// TODO: Handle tunnel error
		}
	}()

	return result.toTunnel(), nil
}

type acceptedConnection struct {
	connection net.Conn
	err        error
}

func (it *internalTunnel) run(id string) error {
	pendingConnection := make(chan acceptedConnection)
	// In the very worst case we might find ourselves/ with an accepted connection
	// in the pendingConnection channel that haven't been read out of it. That's
	// why we are first deferring pendingConnection cleanup and only then
	// deferring listener close (we want to have listener closed and acceptor
	// thread failed with an error before we initiate pendingConnection wipe)
	defer func() {
		select {
		case c := <-pendingConnection:
			if c.connection != nil {
				c.connection.Close()
			}
		default:
		}
	}()
	defer it.listener.Close()

	// Start acceptor goroutine. It accepts incoming connections and sends them
	// to pendingConnection channel.
	go func() {
		for {
			conn, err := it.listener.Accept()
			if err != nil {
				pendingConnection <- acceptedConnection{
					connection: nil,
					err:        err,
				}
				return
			}
			pendingConnection <- acceptedConnection{
				connection: conn,
				err:        nil,
			}
		}
	}()

	activeConnections := make([]connection, 0)
	defer func() {
		// TODO: Cleanup active connections
	}()

	for {
		select {
		case netConn := <-pendingConnection:
			if netConn.err != nil {
				log.Printf("Detected that we are unable to accept connections at %q: %v", id, netConn.err)
				return netConn.err
			}

			log.Printf("Accepted connection at %q", id)

			conn, err := newConnection(netConn.connection, it.connectTo)
			if err != nil {
				log.Printf("Failed to connect to %q: %v", it.connectTo, err)
			} else {
				activeConnections = append(activeConnections, conn)
			}

		case limits := <-it.limitsUpdate:
			log.Printf("Tunnel at %q limits updated: %v", id, limits)
		case <-it.shutdown:
			log.Printf("Tunnel at %q shutting down by a signal from dispatch", id)
			return nil
		case <-it.globalQuit:
			log.Printf("Tunnel at %q shutting down by a global graceful shutdown signal", id)
			return nil
		}
	}
}

type connection struct {
}

func newConnection(ingress net.Conn, connectTo ConnectTo) (connection, error) {
	egress, err := net.Dial("tcp", string(connectTo))
	if err != nil {
		return connection{}, err
	}

	go func() {
		err := forward(ingress, egress, []*rate.Limiter{}, context.Background())
		if err != nil {
			log.Printf("Failed to forward from ingress to egress: %v", err)
		}
	}()

	go func() {
		err := forward(egress, ingress, []*rate.Limiter{}, context.Background())
		if err != nil {
			log.Printf("Failed to forward from ingress to egress: %v", err)
		}
	}()

	return connection{}, nil
}
