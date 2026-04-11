package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"dojo/pkg/dojo"

	"github.com/jackc/pgproto3/v2"
)

// pgConn holds per-connection state for a proxied Postgres connection.
type pgConn struct {
	id        string // correlation ID, set when a matching query arrives
	lastQuery string // most recent SQL query on this connection
	// stmts maps prepared-statement names to their SQL text so that
	// Bind messages (which carry only the name) can be resolved back to
	// the original query for matching.  pgx v5's default CacheStatement
	// mode reuses named statements across queries on the same connection.
	stmts map[string]string
}

// PostgresProxy represents the Observer for Postgres, which proxies to a live DB or mocks responses.
type PostgresProxy struct {
	LiveURL string
	// DialAddr is host:port of the upstream Postgres TCP endpoint when forwarding (live mode).
	// Empty means the proxy terminates the protocol locally (wire-level mock/sniffer).
	DialAddr   string
	addr       string
	listener   net.Listener
	wg         sync.WaitGroup
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	conns      map[net.Conn]*pgConn
	matchTable dojo.MatchTable
	log        *slog.Logger
}

// SetLogger configures the structured logger for the proxy.
func (p *PostgresProxy) SetLogger(l *slog.Logger) {
	p.log = l
}

// NewPostgresProxy initializes a PostgresProxy.
func NewPostgresProxy(liveURL string) *PostgresProxy {
	return &PostgresProxy{
		LiveURL: liveURL,
		conns:   make(map[net.Conn]*pgConn),
		log:     slog.Default(),
	}
}

// Listen implements the dojo.Adapter interface.
func (p *PostgresProxy) Listen(ctx context.Context, matchTable dojo.MatchTable) error {
	return p.Start(ctx, "127.0.0.1:0", matchTable)
}

// Trigger is a no-op for PostgresProxy.
func (p *PostgresProxy) Trigger(ctx context.Context, payload []byte, endpointConfig map[string]any) error {
	return nil
}

// Start boots the PostgresProxy listener on the provided address. The provided context
// controls the proxy lifecycle; cancelling it is equivalent to calling [PostgresProxy.Stop].
func (p *PostgresProxy) Start(ctx context.Context, listenAddr string, matchTable dojo.MatchTable) error {
	p.matchTable = matchTable
	l, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("postgres proxy listen on %s: %w", listenAddr, err)
	}
	p.listener = l
	p.addr = l.Addr().String()
	p.ctx, p.cancel = context.WithCancel(ctx)

	p.wg.Add(1)
	go p.acceptLoop()
	return nil
}

// ExtractPostgresDialAddr returns host:port from a postgres:// URL for net.Dial, or empty if unset or unparseable.
func ExtractPostgresDialAddr(pgURL string) string {
	pgURL = strings.TrimSpace(pgURL)
	if pgURL == "" {
		return ""
	}
	parts := strings.Split(strings.TrimPrefix(pgURL, "postgres://"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return ""
	}
	hostAuth := strings.Split(parts[0], "@")
	if len(hostAuth) > 1 {
		return hostAuth[1]
	}
	return hostAuth[0]
}

// isConnClosed returns true if the error indicates a permanently closed connection.
// EOF is not treated as closed because pgproto3 may return transient EOF during
// live proxying when data arrives in chunks.
func isConnClosed(err error) bool {
	if errors.Is(err, io.ErrClosedPipe) || errors.Is(err, net.ErrClosed) {
		return true
	}
	// pgproto3 wraps errors without preserving the chain, so fall back to string matching.
	msg := err.Error()
	return strings.Contains(msg, "closed") || strings.Contains(msg, "reset by peer")
}

func (p *PostgresProxy) addConn(c net.Conn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.conns[c] = &pgConn{stmts: make(map[string]string)}
}

func (p *PostgresProxy) removeConn(c net.Conn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.conns, c)
}

// ConnCount returns the number of active connections. Exported for testing.
func (p *PostgresProxy) ConnCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.conns)
}

func (p *PostgresProxy) recordQuery(c net.Conn, q string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if pc, ok := p.conns[c]; ok {
		pc.lastQuery = q
	}
}

// responseScanner wraps an io.Reader and forwards response bytes to the
// [MatchTable] for live-mode Postgres matching. The buffer accumulates data
// across reads for the current query and resets only when a new query is
// detected, so multi-chunk responses are fully visible to ProcessResponse.
// Memory stays bounded at [maxResponseBuffer] per query.
type responseScanner struct {
	r             io.Reader
	proxy         *PostgresProxy
	clientConn    net.Conn
	buffer        []byte
	lastSeenQuery string
}

const maxResponseBuffer = 4 * 1024 * 1024 // 4 MiB safety cap per query

func (rs *responseScanner) Read(b []byte) (n int, err error) {
	n, err = rs.r.Read(b)
	if n > 0 {
		rs.proxy.mu.Lock()
		var id, lastQuery string
		if pc, ok := rs.proxy.conns[rs.clientConn]; ok {
			id = pc.id
			lastQuery = pc.lastQuery
		}
		rs.proxy.mu.Unlock()

		if lastQuery != rs.lastSeenQuery {
			rs.buffer = rs.buffer[:0]
			rs.lastSeenQuery = lastQuery
		}

		if len(rs.buffer)+n <= maxResponseBuffer {
			rs.buffer = append(rs.buffer, b[:n]...)
		}

		if id != "" && lastQuery != "" && rs.proxy.matchTable != nil {
			snapshot := make([]byte, len(rs.buffer))
			copy(snapshot, rs.buffer)
			rs.proxy.matchTable.ProcessResponse("postgres", id, "", []byte(lastQuery), snapshot)
		}
	}
	return n, err
}

func (p *PostgresProxy) acceptLoop() {
	defer p.wg.Done()

	for {
		conn, err := p.listener.Accept()
		if err != nil {
			select {
			case <-p.ctx.Done():
				return
			default:
				p.log.Warn("accept error, backing off", "error", err)
				time.Sleep(50 * time.Millisecond)
				continue
			}
		}

		p.wg.Add(1)
		go func(clientConn net.Conn) {
			defer p.wg.Done()
			defer clientConn.Close()
			defer p.removeConn(clientConn)
			p.addConn(clientConn)

			var targetConn net.Conn
			var pw *io.PipeWriter
			var pr *io.PipeReader

			// Forward to upstream only when the suite configured a live Postgres API (DialAddr set by the engine).
			dialAddr := strings.TrimSpace(p.DialAddr)
			isWireMock := dialAddr == ""

			if !isWireMock {
				targetConn, err = net.Dial("tcp", dialAddr)
				if err != nil {
					p.log.Warn("upstream dial failed", "addr", dialAddr, "error", err)
					return
				}
				defer targetConn.Close()
			}

			pr, pw = io.Pipe()

			tee := io.TeeReader(clientConn, pw)

			go func() {
				defer pr.Close()
				defer io.Copy(io.Discard, pr)

				cr := pgproto3.NewChunkReader(pr)
				backend := pgproto3.NewBackend(cr, nil)

				startupMsg, err := backend.ReceiveStartupMessage()
				if err != nil {
					return
				}

				if _, isSSL := startupMsg.(*pgproto3.SSLRequest); isSSL {
					if isWireMock {
						if _, err := clientConn.Write([]byte{'N'}); err != nil {
							p.log.Warn("client write failed", "error", err)
							return
						}
					}
					startupMsg, err = backend.ReceiveStartupMessage()
					if err != nil {
						return
					}
				}

				// writeMsg encodes a pgproto3 message and writes it to the client connection.
				// Returns false on encode or write failure so the caller terminates the
				// connection instead of continuing with a corrupted stream.
				writeMsg := func(msg pgproto3.Message) bool {
					b, err := msg.Encode(nil)
					if err != nil {
						p.log.Warn("message encode failed", "error", err)
						return false
					}
					if _, err := clientConn.Write(b); err != nil {
						p.log.Warn("client write failed", "error", err)
						return false
					}
					return true
				}

				if isWireMock {
					if !writeMsg(&pgproto3.AuthenticationOk{}) {
						return
					}
					if !writeMsg(&pgproto3.ReadyForQuery{TxStatus: 'I'}) {
						return
					}
				}

				for {
					msg, err := backend.Receive()
					if err != nil {
						if isConnClosed(err) {
							return
						}
						p.log.Debug("pgproto3 receive error", "error", err)
						continue
					}

				switch m := msg.(type) {
			case *pgproto3.Query:
				p.recordQuery(clientConn, m.String)
				var mr dojo.MatchResult
				if p.matchTable != nil {
					mr = p.matchTable.ProcessRequest("postgres", "", []byte(m.String), nil, "")
				}

				p.mu.Lock()
				if pc, ok := p.conns[clientConn]; ok {
					pc.id = mr.MatchedID
				}
				p.mu.Unlock()
				if mr.IsMock {
					if !writeMsg(&pgproto3.CommandComplete{CommandTag: []byte("INSERT 0 1")}) {
						return
					}
					if !writeMsg(&pgproto3.ReadyForQuery{TxStatus: 'I'}) {
						return
					}
				}
			case *pgproto3.Parse:
				p.recordQuery(clientConn, m.Query)
				var mr dojo.MatchResult
				if p.matchTable != nil {
					mr = p.matchTable.ProcessRequest("postgres", "", []byte(m.Query), nil, "")
				}

				p.mu.Lock()
				if pc, ok := p.conns[clientConn]; ok {
					pc.id = mr.MatchedID
					if m.Name != "" {
						pc.stmts[m.Name] = m.Query
					}
				}
				p.mu.Unlock()
				if mr.IsMock {
					if !writeMsg(&pgproto3.ParseComplete{}) {
						return
					}
				}
					case *pgproto3.Bind:
						bindMocked := false
						p.mu.Lock()
						var resolvedSQL string
						if pc, ok := p.conns[clientConn]; ok {
							resolvedSQL = pc.stmts[m.PreparedStatement]
						}
						p.mu.Unlock()

						if resolvedSQL != "" {
							p.recordQuery(clientConn, resolvedSQL)
							var mr dojo.MatchResult
							if p.matchTable != nil {
								mr = p.matchTable.ProcessRequest("postgres", "", []byte(resolvedSQL), nil, "")
							}
							p.mu.Lock()
							if pc, ok := p.conns[clientConn]; ok {
								pc.id = mr.MatchedID
							}
							p.mu.Unlock()
							if mr.IsMock {
								if !writeMsg(&pgproto3.BindComplete{}) {
									return
								}
								bindMocked = true
							}
						}
						if isWireMock && !bindMocked {
							if !writeMsg(&pgproto3.BindComplete{}) {
								return
							}
						}
					case *pgproto3.Execute:
						if isWireMock {
							if !writeMsg(&pgproto3.CommandComplete{CommandTag: []byte("INSERT 0 1")}) {
								return
							}
						}
					case *pgproto3.Sync:
						if isWireMock {
							if !writeMsg(&pgproto3.ReadyForQuery{TxStatus: 'I'}) {
								return
							}
						}
					case *pgproto3.Terminate:
						return
					}
				}
			}()

			defer pw.Close()

		if !isWireMock {
			go func() {
				<-p.ctx.Done()
				clientConn.Close()
				targetConn.Close()
			}()

			go func() {
				if _, err := io.Copy(targetConn, tee); err != nil && !isConnClosed(err) && p.ctx.Err() == nil {
					p.log.Warn("client→upstream copy error", "error", err)
				}
			}()

			scanner := &responseScanner{
				r:          targetConn,
				proxy:      p,
				clientConn: clientConn,
			}
			if _, err := io.Copy(clientConn, scanner); err != nil && !isConnClosed(err) && p.ctx.Err() == nil {
				p.log.Warn("upstream→client copy error", "error", err)
			}
		} else {
			if _, err := io.Copy(io.Discard, tee); err != nil && !isConnClosed(err) {
				p.log.Warn("wire-mock tee drain error", "error", err)
			}
		}
		}(conn)
	}
}

// Stop terminates the proxy listener and closes all active connections so
// that I/O goroutines unblock and the WaitGroup drains.
func (p *PostgresProxy) Stop() error {
	if p.cancel != nil {
		p.cancel()
	}
	if p.listener != nil {
		p.listener.Close()
	}
	p.mu.Lock()
	for c := range p.conns {
		c.Close()
	}
	p.mu.Unlock()
	p.wg.Wait()
	return nil
}

// Addr returns the listener address.
func (p *PostgresProxy) Addr() string {
	return p.addr
}
