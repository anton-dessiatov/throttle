package app

import (
	"context"
	"log"
	"net"
	"sync"
	"time"

	"github.com/anton-dessiatov/throttle/limiter"
)

// TunnelLimits encapsulates bandwidth limits for a given tunnel.
type TunnelLimits struct {
	// Overall tunnel bandwidth limit. Total bandwidth usage by a tunnel never
	// exceeds this value
	TunnelLimit Limit
	// Bandwidth limit for individual connections of this tunnel. No single
	// connection made as a part of this tunnel is allowed to exceed this limit.
	ConnectionLimit Limit
}

// Tunnel is a structure that contains everything you might need to manage an
// existing TCP tunnel
type Tunnel struct {
	listenAt      ListenAt
	connectTo     ConnectTo
	shutdown      chan struct{}
	listener      *limiter.RateLimitingListener
	currentLimits limiter.RateLimits
	updateLimits  chan limiter.RateLimits
	waitGroup     *sync.WaitGroup
}

// UpdateLimits sets new bandwidth limits for a tunnel. All active connections
// of given tunnel are notified and have their limits updated as well.
func (t Tunnel) UpdateLimits(newLimits limiter.RateLimits) {
	select {
	case t.updateLimits <- newLimits:
	case <-t.shutdown:
	}
}

// Shutdown shuts the tunnel down and blocks until shutdown process is complete.
// This means waiting until all connections and listening socket get close.
func (t Tunnel) Shutdown() {
	close(t.shutdown)
	t.waitGroup.Wait()
}

// NewTunnel creates a traffic forwarding tunnel with a given listen port
// spec and configuration. Inbound connection listening begins immediately.
func NewTunnel(listenAt ListenAt, connectTo ConnectTo, limits limiter.RateLimits) (*Tunnel, error) {
	shutdown := make(chan struct{})
	updateLimitsChan := make(chan limiter.RateLimits)
	wg := new(sync.WaitGroup)

	log.Printf("Starting tunnel at %q", listenAt)

	l, err := net.Listen("tcp", string(listenAt))
	if err != nil {
		log.Printf("Failed to listen at %q: %v", listenAt, err)
		return nil, err
	}
	// It's internal Tunnel's run() responsibility to close the listener
	result := &Tunnel{
		listenAt:      listenAt,
		connectTo:     connectTo,
		shutdown:      shutdown,
		listener:      limiter.NewRateLimitingListener(l, limits),
		currentLimits: limits,
		updateLimits:  updateLimitsChan,
		waitGroup:     wg,
	}

	wg.Add(1)
	go func() {
		defer wg.Done()

		retry := make(chan struct{})

		for {
			if result.listener != nil {
				err := result.run()
				if err == nil {
					return
				}
				// err is not nil, which means that there was an error trying to accept
				// connection. This means that listening socket is no longer in a valid
				// state. Retry listening
				err = result.listener.Close()
				if err != nil {
					log.Printf("Failed to close listening socket for %q after discovering "+
						"accept failure: %v", listenAt, err)
					// Don't exit, try to recover anyways.
				}
				result.listener = nil
				log.Printf("Failed to accept connection on listener %q: %v", listenAt, err)
			}

			// Wait a bit before trying to recreate listener socket
			go func() {
				time.Sleep(5 * time.Second)
				retry <- struct{}{}
			}()

			select {
			case <-retry:
				l, err := net.Listen("tcp", string(listenAt))
				if err != nil {
					log.Printf("Failed to listen at %q: %v", listenAt, err)
				} else {
					result.listener = limiter.NewRateLimitingListener(l, result.currentLimits)
				}
			case <-shutdown:
				log.Printf("Detected tunnel shutdown while retrying listening at %q", listenAt)
				return
			} // select
		} // for
	}()

	return result, nil
}

type acceptedConnection struct {
	connection net.Conn
	err        error
}

func (t *Tunnel) run() error {
	pendingConnection := make(chan acceptedConnection)
	// In the very worst case we might find ourselves with an accepted connection
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
	defer t.listener.Close()

	// Start acceptor goroutine. It accepts incoming connections and sends them
	// to pendingConnection channel.
	go func() {
		for {
			conn, err := t.listener.Accept()
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

	activeConnections := make(map[*Connection]struct{})
	completeChan := make(chan connectionComplete)
	defer func() {
		for conn := range activeConnections {
			conn.Close()
		}
	}()

	for {
		select {
		case netConn := <-pendingConnection:
			if netConn.err != nil {
				// We were unable to accept connection. I believe it's safe to assume
				// that listening socket is no longer alive and therefore all
				// connections previously accepted on that socket are dead as well.
				// Which means it's probably safe to return (shutdown all active
				// connections and try to reestablish the listener)
				log.Printf("Detected that we are unable to accept connection at %q: %v",
					t.listenAt, netConn.err)
				return netConn.err
			}

			log.Printf("Accepted connection at %q", t.listenAt)

			conn := NewConnection(netConn.connection, t.connectTo)
			connDone, err := conn.Run()
			if err != nil {
				log.Printf("Failed to connect to %q: %v", t.connectTo, err)
				netConn.connection.Close()
			} else {
				activeConnections[conn] = struct{}{}
				go func(conn *Connection, connDone chan error) {
					for v := range connDone {
						select {
						case completeChan <- connectionComplete{
							connection: conn,
							err:        v,
						}:
						case <-t.shutdown:
							return
						}
					}
				}(conn, connDone)
			}

		case complete := <-completeChan:
			if complete.err != nil {
				log.Printf("Connection completed with failure: %v", complete.err)
			}
			_, ok := activeConnections[complete.connection]
			if ok {
				delete(activeConnections, complete.connection)
				complete.connection.Close()
				log.Printf("Closed connection at %q", t.listenAt)
			}

		case limits := <-t.updateLimits:
			t.listener.UpdateLimits(limits)
			log.Printf("Tunnel at %q limits updated: %v", t.listenAt, limits)

		case <-t.shutdown:
			log.Printf("Tunnel at %q shutting down", t.listenAt)
			return nil
		} // select
	} // for
}

// Connection ensapsulates a single traffic forwarding connection within a
// tunnel.
type Connection struct {
	ctx       context.Context
	ctxCancel func()

	ingress   net.Conn
	connectTo ConnectTo
	egress    net.Conn
}

type connectionComplete struct {
	connection *Connection
	err        error
}

// NewConnection creates a connection with given ingress, destination and
// limits. In order to actually start forwarding traffic, call Run() on a
// created connection.
//
// Beware that created Connection takes ownership of an ingress net.Conn and
// closes it when gets closed.
func NewConnection(ingress net.Conn, connectTo ConnectTo) *Connection {
	ctx, ctxCancel := context.WithCancel(context.Background())
	return &Connection{
		ctx:       ctx,
		ctxCancel: ctxCancel,

		ingress:   ingress,
		connectTo: connectTo,
	}
}

// Close closes Connection. This results in canceling all pending operations and
// closing both ingress and egress network connections.
func (c Connection) Close() {
	c.ctxCancel()
	if c.egress != nil {
		err := c.egress.Close()
		if err != nil {
			log.Printf("Failed to close egress connection: %v", err)
		}
	}
	err := c.ingress.Close()
	if err != nil {
		log.Printf("Failed to close ingress connection: %v", err)
	}
}

// Run performs traffic tunneling for a connection. It creates a socket
// connected to an address given in connectTo argument and starts forwarding
// traffic between ingress and destination.
//
// For each Connection, Run might only be invoked on a single goroutine
// simultaneously. Attempts to Run single connection multiple times
// concurrently will fail.
func (c *Connection) Run() (chan error, error) {
	resultChan := make(chan error)
	var err error
	c.egress, err = net.Dial("tcp", string(c.connectTo))
	if err != nil {
		return nil, err
	}

	ingressForwarder := CreateForwarder(c.ingress, c.egress)
	go func() {
		err := ingressForwarder.Run(c.ctx)
		select {
		case resultChan <- err:
		case <-c.ctx.Done():
		}
	}()

	egressForwarder := CreateForwarder(c.egress, c.ingress)
	go func() {
		err := egressForwarder.Run(c.ctx)
		select {
		case resultChan <- err:
		case <-c.ctx.Done():
		}
	}()

	return resultChan, nil
}
