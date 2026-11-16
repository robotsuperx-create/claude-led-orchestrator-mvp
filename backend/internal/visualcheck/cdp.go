package visualcheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/coder/websocket"
)

// maxCDPMessage bounds one DevTools message; a full-page screenshot arrives
// as one base64 string.
const maxCDPMessage = 64 << 20

// cdpClient speaks the Chrome DevTools Protocol over one websocket: calls are
// matched to their answers by id, and everything else is an event.
type cdpClient struct {
	conn    *websocket.Conn
	nextID  atomic.Int64
	mu      sync.Mutex
	pending map[int64]chan cdpMessage
	err     error
	events  chan cdpMessage
	done    chan struct{}
	// stop ends the read loop when the client is closed.
	stop context.CancelFunc
}

type cdpMessage struct {
	ID        int64           `json:"id,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Method    string          `json:"method,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *cdpError       `json:"error,omitempty"`
}

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

var errCDPClosed = errors.New("the browser connection closed")

func dialCDP(ctx context.Context, url string) (*cdpClient, error) {
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to the browser: %w", err)
	}
	conn.SetReadLimit(maxCDPMessage)
	// The connection outlives the dial's deadline; close() ends it.
	readCtx, stop := context.WithCancel(context.WithoutCancel(ctx))
	client := &cdpClient{
		stop:    stop,
		conn:    conn,
		pending: make(map[int64]chan cdpMessage),
		events:  make(chan cdpMessage, 512),
		done:    make(chan struct{}),
	}
	go client.read(readCtx)
	return client, nil
}

func (c *cdpClient) read(ctx context.Context) {
	defer close(c.done)
	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			c.mu.Lock()
			c.err = errCDPClosed
			c.mu.Unlock()
			return
		}
		var message cdpMessage
		if json.Unmarshal(data, &message) != nil {
			continue
		}
		if message.ID != 0 {
			c.mu.Lock()
			answer := c.pending[message.ID]
			delete(c.pending, message.ID)
			c.mu.Unlock()
			if answer != nil {
				answer <- message
			}
			continue
		}
		// Events are advisory (load finished, a console error); when nobody
		// is draining them, the oldest matter least.
		select {
		case c.events <- message:
		default:
		}
	}
}

// call sends method to the browser, or to the page attached as session, and
// decodes its answer into result when result is not nil.
func (c *cdpClient) call(ctx context.Context, session, method string, params, result any) error {
	id := c.nextID.Add(1)
	request := map[string]any{"id": id, "method": method}
	if params != nil {
		request["params"] = params
	}
	if session != "" {
		request["sessionId"] = session
	}
	data, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	answer := make(chan cdpMessage, 1)
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return c.err
	}
	c.pending[id] = answer
	c.mu.Unlock()
	forget := func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}
	if err := c.conn.Write(ctx, websocket.MessageText, data); err != nil {
		forget()
		return fmt.Errorf("%s: %w", method, err)
	}
	select {
	case message := <-answer:
		if message.Error != nil {
			return fmt.Errorf("%s: %s", method, message.Error.Message)
		}
		if result != nil && len(message.Result) > 0 {
			if err := json.Unmarshal(message.Result, result); err != nil {
				return fmt.Errorf("%s: %w", method, err)
			}
		}
		return nil
	case <-c.done:
		return errCDPClosed
	case <-ctx.Done():
		forget()
		return ctx.Err()
	}
}

// drain discards queued events, so a wait sees only what happens after it.
func (c *cdpClient) drain() {
	for {
		select {
		case <-c.events:
		default:
			return
		}
	}
}

func (c *cdpClient) close() {
	_ = c.conn.Close(websocket.StatusNormalClosure, "")
	c.stop()
}
