package udp

import (
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Set of error variables for start up.
var (
	ErrInvalidConfiguration = errors.New("Invalid Configuration")
	ErrInvalidNetType       = errors.New("Invalid NetType Configuration")
	ErrInvalidConnHandler   = errors.New("Invalid Connection Handler Configuration")
	ErrInvalidReqHandler    = errors.New("Invalid Request Handler Configuration")
	ErrInvalidRespHandler   = errors.New("Invalid Response Handler Configuration")
)

// temporary is declared to test for the existence of the method coming
// from the net package.
type temporary interface {
	Temporary() bool
}

// UDP manages message to a specific ip address and port.
type UDP struct {
	Config
	Name string

	ipAddress string
	port      int
	udpAddr   *net.UDPAddr

	listener   *net.UDPConn
	listenerMu sync.RWMutex

	reader io.Reader
	writer io.Writer

	wg           sync.WaitGroup
	shuttingDown int32
}

// New creates a new manager to service clients.
func New(name string, cfg Config) (*UDP, error) {

	// Validate the configuration.
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	// Resolve the addr that is provided.
	udpAddr, err := net.ResolveUDPAddr(cfg.NetType, cfg.Addr)
	if err != nil {
		return nil, err
	}

	// Create a UDP for this ipaddress and port.
	udp := UDP{
		Config: cfg,
		Name:   name,

		ipAddress: udpAddr.IP.String(),
		port:      udpAddr.Port,
		udpAddr:   udpAddr,
	}

	return &udp, nil
}

// join takes an IP and port values and creates a cleaner string.
func join(ip string, port int) string {
	return net.JoinHostPort(ip, strconv.Itoa(port))
}

// Start begins to accept data.
func (d *UDP) Start() error {
	d.listenerMu.Lock()
	{
		// If the listener has been started already, return an error.
		if d.listener != nil {
			d.listenerMu.Unlock()
			return errors.New("this UDP has already been started")
		}
	}
	d.listenerMu.Unlock()

	// We need to wait for the goroutine to initialize itself.
	var waitStart sync.WaitGroup
	waitStart.Add(1)

	// Start the data accept routine.
	d.wg.Add(1)
	go func() {
		for {
			d.listenerMu.Lock()
			{
				// Start a listener for the specified addr and port is one
				// does not exist.
				if d.listener == nil {
					var err error
					d.listener, err = net.ListenUDP(d.NetType, d.udpAddr)
					if err != nil {
						panic(err)
					}

					// Ask the user to bind the reader and writer they want to
					// use for this listener.
					d.reader, d.writer = d.ConnHandler.Bind(d.listener)

					waitStart.Done()

					d.Event("accept", "Waiting For Data : IPAddress[ %s ]", join(d.ipAddress, d.port))
				}
			}
			d.listenerMu.Unlock()

			// Wait for a message to arrive.
			udpAddr, data, length, err := d.ReqHandler.Read(d.reader)
			timeRead := time.Now()

			if err != nil {
				if atomic.LoadInt32(&d.shuttingDown) == 1 {
					d.listenerMu.Lock()
					{
						d.listener = nil
					}
					d.listenerMu.Unlock()
					break
				}

				d.Event("accept", "ERROR : %v", err)

				if e, ok := err.(temporary); ok && !e.Temporary() {
					d.listenerMu.Lock()
					{
						d.listener.Close()
						d.listener = nil
					}
					d.listenerMu.Unlock()

					// Don't want to add a flag. So setting this back to
					// 1 so when the listener is re-established, the call
					// to Done does not fail.
					waitStart.Add(1)
				}

				continue
			}

			// Check to see if this message is ipv6.
			isIPv6 := true
			if ip4 := udpAddr.IP.To4(); ip4 != nil {

				// Make sure we return an IPv4 address if udpAddr
				// is an IPv4-mapped IPv6 address.  Otherwise we
				// could end up sending an IPv6 response.
				udpAddr.IP = ip4
				isIPv6 = false
			}

			// Create the request.
			req := Request{
				UDP:     d,
				UDPAddr: udpAddr,
				IsIPv6:  isIPv6,
				ReadAt:  timeRead,
				Data:    data,
				Length:  length,
			}

			// Process the request on this goroutine that is
			// handling the socket connection.
			d.ReqHandler.Process(&req)
		}

		d.wg.Done()
		d.Event("accept", "Shutdown : IPAddress[ %s ]", join(d.ipAddress, d.port))

		return
	}()

	// Wait for the goroutine to initialize itself.
	waitStart.Wait()

	return nil
}

// Stop shuts down the manager and closes all connections.
func (d *UDP) Stop() error {
	d.listenerMu.Lock()
	{
		// If the listener has been stopped already, return an error.
		if d.listener == nil {
			d.listenerMu.Unlock()
			return errors.New("this UDP has already been stopped")
		}
	}
	d.listenerMu.Unlock()

	// Mark that we are shutting down.
	atomic.StoreInt32(&d.shuttingDown, 1)

	// Don't accept anymore client data.
	d.listenerMu.Lock()
	{
		d.listener.Close()
	}
	d.listenerMu.Unlock()

	// Wait for the accept routine to terminate.
	d.wg.Wait()

	return nil
}

// Send will deliver the response back to the client.
func (d *UDP) Send(r *Response) error {
	return d.RespHandler.Write(r, d.writer)
}

// Addr returns the local listening network address.
func (d *UDP) Addr() net.Addr {

	// We are aware this read is not safe with the
	// goroutine accepting connections.
	if d.listener == nil {
		return nil
	}
	return d.listener.LocalAddr()
}
