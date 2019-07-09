package c37splitter

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/binary"
	"io"
	"net"
	"os"
	"sync"
	"time"

	logging "github.com/op/go-logging"
)

var lg *logging.Logger

func init() {
	logging.SetBackend(logging.NewLogBackend(os.Stdout, "", 0))
	logging.SetFormatter(logging.MustStringFormatter("[%{level}]%{time} > %{message}"))
	lg = logging.MustGetLogger("log")
}

type SplitterConfig struct {
	//If false, the upstream connection will be established by listening
	//for a TCP connection on the given address. Only one connection will be
	//accepted
	DialUpstream          bool   `yaml:"dialUpstream"`
	UpstreamListenTLS     bool   `yaml:"upstreamListenTLS"`
	UpstreamListenAddress string `yaml:"upstreamListenAddress"`
	//If DialUpstream is true, this is the address we will attempt to dial
	UpstreamDialAddress string `yaml:"upstreamDialAddress"`

	//If true, additional downstream channels will be created on-the-fly
	//for connections made to DownstreamListenAddress
	ListenDownstream           bool   `yaml:"listenDownstream"`
	DownstreamListenAddress    string `yaml:"downstreamListenAddress"`
	ListenDownstreamTLS        bool   `yaml:"listenDownstreamTLS"`
	DownstreamListenAddressTLS string `yaml:"downstreamListenAddressTLS"`

	//This is used for listening TLS, both upstream and downstream
	Certificate string `yaml:"certificate"`
	Key         string `yaml:"key"`

	//These are the addresses we dial to pass traffic to
	DialDownstreamAddresses []string `yaml:"dialDownstreamAddresses"`
	//These are the addresses we dial to pass traffic to, using TLS
	DialDownstreamAddressesTLS []string `yaml:"dialDownstreamAddressesTLS"`
}

//A splitter manages upstream and downstream connections, proxying the
//upstream to the downstream
type Splitter struct {
	cfg *SplitterConfig
	//frames can be written to this to be sent upstream
	upstream chan []byte
	//Locked when a downstream needs to be removed
	mu sync.Mutex
	//a map of channels that frames will be written to by upstream
	downstreams map[string]chan []byte
}

//Start the splitter
func StartSplitter(cfg *SplitterConfig) *Splitter {
	rv := &Splitter{
		cfg:         cfg,
		upstream:    make(chan []byte, 10),
		downstreams: make(map[string]chan []byte),
	}
	lg.Infof("starting downstream connections")
	rv.beginDownstreamConnections()
	lg.Infof("starting upstream connections")
	rv.beginUpstreamConnection()
	return rv
}

//Proxy a downstream connection, returning an error upon connection
//close
func (s *Splitter) proxyDownstream(id string, conn net.Conn) {
	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		s.mu.Lock()
		delete(s.downstreams, id)
		s.mu.Unlock()
	}()
	rdr := bufio.NewReader(conn)
	dchan := make(chan []byte, 100)
	s.mu.Lock()
	s.downstreams[id] = dchan
	s.mu.Unlock()
	go func() {
		for {
			if ctx.Err() != nil {
				return
			}
			inframe, err := readFrame(rdr)
			if err != nil {
				lg.Errorf("[downstream/%s] read error: %v", id, err)
				cancel()
				return
			}
			s.upstream <- inframe
		}
	}()
	for {
		select {
		case frame := <-dchan:
			_, err := conn.Write(frame)
			if err != nil {
				lg.Errorf("[downstream/%s] write error: %v", id, err)
				cancel()
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

//Start the goroutines that do downstream
func (s *Splitter) beginDownstreamConnections() {
	if s.cfg.ListenDownstream {
		go func() {
			//This will panic if there is an error
			s.listenDownstream(s.cfg.DownstreamListenAddress)
		}()
	}
	if s.cfg.ListenDownstreamTLS {
		go func() {
			s.listenDownstreamTLS(s.cfg.DownstreamListenAddressTLS, s.cfg.Certificate, s.cfg.Key)
		}()
	}
	//For each downstream address, dial it in a loop
	for _, ds := range s.cfg.DialDownstreamAddresses {
		go func(ds string) {
			for {
				//This already logs any error
				s.dialDownstream(ds)
				time.Sleep(5 * time.Second)
			}
		}(ds)
	}
}

//Start a listening socket for downstream connections
func (s *Splitter) listenDownstream(address string) {
	var conn *net.TCPConn
	addr, err := net.ResolveTCPAddr("tcp", address)
	if err != nil {
		lg.Fatalf("could not resolve downstream listen address: %v", err)
	}
	listener, err := net.ListenTCP("tcp", addr)
	if err != nil {
		lg.Fatalf("could not initiate downstream listening: %v", err)
	}
	for {
		conn, err = listener.AcceptTCP()
		if err != nil {
			lg.Warning("failed to accept connection: %v", err)
			continue
		}
		lg.Infof("[downstream/listen] accepted downstream connection from %s", conn.RemoteAddr().String())
		go func() {
			s.proxyDownstream("incoming/"+conn.RemoteAddr().String(), conn)
		}()
	}
}

//Start a listening socket for downstream connections with TLS
func (s *Splitter) listenDownstreamTLS(address string, cert, key string) {
	var conn net.Conn

	cer, err := tls.LoadX509KeyPair(cert, key)
	if err != nil {
		lg.Fatalf("could not load certificate: %v", err)

	}

	config := &tls.Config{Certificates: []tls.Certificate{cer}}

	listener, err := tls.Listen("tcp", address, config)
	if err != nil {
		lg.Fatalf("could not initiate downstream listening: %v", err)
	}
	for {
		conn, err = listener.Accept()
		if err != nil {
			lg.Warning("failed to accept connection: %v", err)
			continue
		}
		lg.Infof("[downstream/listen.tls] accepted downstream connection from %s", conn.RemoteAddr().String())
		go func() {
			s.proxyDownstream("incoming/"+conn.RemoteAddr().String(), conn)
		}()
	}
}

//Initiates a connected with a downstream peer
func (s *Splitter) dialDownstream(target string) {
	var conn *net.TCPConn
	defer func() {
		if conn != nil {
			conn.Close()
		}
	}()
	addr, err := net.ResolveTCPAddr("tcp", target)
	if err != nil {
		lg.Errorf("[downstream/%s] could not resolve address: %v", target, err)
		return
	}
	conn, err = net.DialTCP("tcp", nil, addr)
	if err != nil {
		lg.Errorf("[downstream/%s] could not connect: %v", target, err)
		return
	}
	lg.Infof("[downstream/%s] connection initiated", target)

	s.proxyDownstream("outgoing/"+addr.String(), conn)
}

func (s *Splitter) dialDownstreamTLS(target string) {
	var conn *tls.Conn
	defer func() {
		if conn != nil {
			conn.Close()
		}
	}()
	conn, err := tls.Dial("tcp", target, &tls.Config{})
	if err != nil {
		lg.Warningf("[downstream/%s] dial failed", target)
		return
	}
	lg.Infof("[downstream/%s] downstream dial succeeded\n", target)
	s.proxyDownstream("outgoing/"+target, conn)
}

//Process the upstream connection
func (s *Splitter) beginUpstreamConnection() {
	for {
		//Both of these log upon error
		if s.cfg.DialUpstream {
			s.dialUpstream()
		} else {
			//These don't return
			if s.cfg.UpstreamListenTLS {
				s.listenUpstreamTLS()
			} else {
				s.listenUpstream()
			}
		}
		time.Sleep(5 * time.Second)
	}
}

//Read in a complete C37.118 frame
func readFrame(in *bufio.Reader) ([]byte, error) {
	initialByte, err := in.ReadByte()
	if err != nil {
		return nil, err
	}
	skipped := 0
	for initialByte != 0xAA {
		skipped++
		initialByte, err = in.ReadByte()
		if err != nil {
			return nil, err
		}
	}
	if skipped != 0 {
		lg.Warningf("skipped %d bytes resyncing C37 frame", skipped)
	}

	hdr := [14]byte{}
	_, err = io.ReadFull(in, hdr[1:])
	if err != nil {
		return nil, err
	}
	hdr[0] = initialByte
	fsize := binary.BigEndian.Uint16(hdr[2:4])
	fullframe := make([]byte, fsize)
	copy(fullframe[:14], hdr[:])
	_, err = io.ReadFull(in, fullframe[14:])
	if err != nil {
		return nil, err
	}
	return fullframe, nil
}

//Process an upstream connection
func (s *Splitter) proxyUpstream(out net.Conn, in *bufio.Reader) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case frame := <-s.upstream:
				_, err := out.Write(frame)
				if err != nil {
					lg.Errorf("[upstream] write error: %v", err)
					cancel()
					return
				}
			}
		}
	}()
	for {
		if ctx.Err() != nil {
			return
		}
		buf, err := readFrame(in)
		if err != nil {
			lg.Errorf("[upstream] read error: %v", err)
			cancel()
			return
		}
		s.mu.Lock()
		for desc, ch := range s.downstreams {
			select {
			case ch <- buf:
			default:
				lg.Warningf("[downstream/%s] dropping frames", desc)
			}
		}
		s.mu.Unlock()
	}
}

//Initiate an upstream connection
func (s *Splitter) dialUpstream() {
	var conn *net.TCPConn
	defer func() {
		if conn != nil {
			conn.Close()
		}
	}()
	addr, err := net.ResolveTCPAddr("tcp", s.cfg.UpstreamDialAddress)
	if err != nil {
		lg.Errorf("[upstream] could not resolve target address: %v", err)
		return
	}
	lg.Infof("[upstream] attempting connection to: %s", addr)
	conn, err = net.DialTCP("tcp", nil, addr)
	if err != nil {
		lg.Errorf("[upstream] could not connect to target: %v", err)
		return
	}
	lg.Infof("[upstream] dial succeeded")
	br := bufio.NewReader(conn)

	//Proxy upstream
	s.proxyUpstream(conn, br)
}

func (s *Splitter) listenUpstream() {
	var conn *net.TCPConn

	addr, err := net.ResolveTCPAddr("tcp", s.cfg.UpstreamListenAddress)
	if err != nil {
		lg.Errorf("[upstream] could not resolve listen address: %v", err)
		return
	}
	lg.Infof("[upstream] listening for connections on %s", addr)
	listener, err := net.ListenTCP("tcp", addr)
	if err != nil {
		lg.Errorf("[upstream] could not open listen socket: %v", err)
		return
	}
	for {
		conn, err = listener.AcceptTCP()
		if err != nil {
			lg.Errorf("[upstream] listen accept error: %v", err)
			return
		}
		br := bufio.NewReader(conn)
		s.proxyUpstream(conn, br)
		conn.Close()
	}
}

func (s *Splitter) listenUpstreamTLS() {
	var conn net.Conn

	cer, err := tls.LoadX509KeyPair(s.cfg.Certificate, s.cfg.Key)
	if err != nil {
		lg.Fatalf("could not load certificate: %v", err)
	}

	config := &tls.Config{Certificates: []tls.Certificate{cer}}

	listener, err := tls.Listen("tcp", s.cfg.UpstreamListenAddress, config)
	if err != nil {
		lg.Fatalf("could not initiate upstream listening: %v", err)
	}
	for {
		conn, err = listener.Accept()
		if err != nil {
			lg.Warning("failed to accept connection: %v", err)
			return
		}

		lg.Infof("[upstream/listen.tls] accepted upstream connection from %s", conn.RemoteAddr().String())
		br := bufio.NewReader(conn)
		s.proxyUpstream(conn, br)
		conn.Close()
	}
}
