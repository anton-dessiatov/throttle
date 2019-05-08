package app

import (
	"context"
	"log"
	"net"
	"sync"
	"time"

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
	waitGroup    *sync.WaitGroup
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
func CreateTunnel(listenAt ListenAt, connectTo ConnectTo, limits TunnelLimits, wg *sync.WaitGroup) (Tunnel, error) {
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
		waitGroup:    wg,
	}

	wg.Add(1)
	go func() {
		defer wg.Done()

		retry := make(chan struct{})

		for {
			if result.listener != nil {
				err := result.run(string(listenAt))
				if err == nil {
					return
				}
				// err is not nil, which means that there was an error trying to accept
				// connection. This means that listening socket is no longer in a valid
				// state. Retry listening
				log.Printf("Failed to accept connection on listener %q: %v", listenAt, err)
			}

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
					result.listener = l
				}
			case <-shutdown:
				log.Printf("Detected tunnel shutdown while retrying listening at %q", listenAt)
				return
			}
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

	activeConnections := make(map[*connection]struct{})
	completeChan := make(chan connectionComplete)
	defer func() {
		for conn := range activeConnections {
			conn.close()
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
				log.Printf("Detected that we are unable to accept connection at %q: %v", id, netConn.err)
				return netConn.err
			}

			log.Printf("Accepted connection at %q", id)

			conn, err := newConnection{
				ingress:   netConn.connection,
				connectTo: it.connectTo,
				waitGroup: it.waitGroup,
				complete:  completeChan,
			}.create()
			if err != nil {
				log.Printf("Failed to connect to %q: %v", it.connectTo, err)
				netConn.connection.Close()
			} else {
				activeConnections[conn] = struct{}{}
			}
		case complete := <-completeChan:
			if complete.err != nil {
				log.Printf("Connection completed with failure: %v", complete.err)
			}
			_, ok := activeConnections[complete.connection]
			if ok {
				delete(activeConnections, complete.connection)
				complete.connection.close()
				log.Printf("Closed connection at %q", id)
			}
		case limits := <-it.limitsUpdate:
			log.Printf("Tunnel at %q limits updated: %v", id, limits)
		case <-it.shutdown:
			log.Printf("Tunnel at %q shutting down", id)
			return nil
		}
	}
}

type connection struct {
	close func()
}

type connectionComplete struct {
	connection *connection
	err        error
}

type newConnection struct {
	ingress   net.Conn
	connectTo ConnectTo
	waitGroup *sync.WaitGroup
	complete  chan<- connectionComplete
}

func (c newConnection) create() (*connection, error) {
	egress, err := net.Dial("tcp", string(c.connectTo))
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := &connection{
		close: func() {
			cancel()
			err := egress.Close()
			if err != nil {
				log.Printf("Failed to close egress connection: %v", err)
			}
			err = c.ingress.Close()
			if err != nil {
				log.Printf("Failed to close ingress connection: %v", err)
			}
		},
	}

	c.waitGroup.Add(1)
	go func() {
		defer c.waitGroup.Done()
		err := forward{
			from:     c.ingress,
			to:       egress,
			limiters: []*rate.Limiter{},
		}.run(ctx)
		if err != nil {
			log.Printf("Failed to forward ingress to egress: %v", err)
		}
		c.complete <- connectionComplete{
			connection: result,
			err:        err,
		}
	}()

	c.waitGroup.Add(1)
	go func() {
		defer c.waitGroup.Done()
		err := forward{
			from:     egress,
			to:       c.ingress,
			limiters: []*rate.Limiter{},
		}.run(ctx)
		if err != nil {
			log.Printf("Failed to forward egress to ingress: %v", err)
		}
		c.complete <- connectionComplete{
			connection: result,
			err:        err,
		}
	}()

	return result, nil
}
