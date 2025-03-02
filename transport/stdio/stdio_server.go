package stdio

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"sync"

	"github.com/metoro-io/mcp-golang/transport"
	"github.com/metoro-io/mcp-golang/transport/stdio/internal/stdio"
)

// StdioServerTransport implements server-side transport for stdio communication
type StdioServerTransport struct {
	mu        sync.Mutex
	started   bool
	reader    *bufio.Reader
	writer    io.Writer
	readBuf   *stdio.ReadBuffer
	onClose   func()
	onError   func(error)
	onMessage func(ctx context.Context, message *transport.BaseJsonRpcMessage)
	logger    *log.Logger
}

// NewStdioServerTransport creates a new StdioServerTransport using os.Stdin and os.Stdout
func NewStdioServerTransport() *StdioServerTransport {
	return NewStdioServerTransportWithIO(os.Stdin, os.Stdout)
}

// NewStdioServerTransportWithIO creates a new StdioServerTransport with custom io.Reader and io.Writer
func NewStdioServerTransportWithIO(in io.Reader, out io.Writer) *StdioServerTransport {
	return &StdioServerTransport{
		reader:  bufio.NewReader(in),
		writer:  out,
		readBuf: stdio.NewReadBuffer(),
	}
}

// SetLogger sets the logger for the transport and its ReadBuffer
func (t *StdioServerTransport) SetLogger(logger *log.Logger) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.logger = logger
	t.readBuf.SetLogger(logger)
}

// Start begins listening for messages on stdin
func (t *StdioServerTransport) Start(ctx context.Context) error {
	t.mu.Lock()
	if t.started {
		t.mu.Unlock()
		return fmt.Errorf("StdioServerTransport already started")
	}
	t.started = true
	t.mu.Unlock()

	if t.logger != nil {
		t.logger.Println("StdioServerTransport started")
	}

	go t.readLoop(ctx)
	return nil
}

// Close stops the transport and cleans up resources
func (t *StdioServerTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.started = false
	t.readBuf.Clear()
	if t.onClose != nil {
		t.onClose()
	}

	if t.logger != nil {
		t.logger.Println("StdioServerTransport closed")
	}

	return nil
}

// Send sends a JSON-RPC message
func (t *StdioServerTransport) Send(ctx context.Context, message *transport.BaseJsonRpcMessage) error {
	data, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}
	data = append(data, '\n')

	//println("serialized message:", string(data))
	if t.logger != nil {
		t.logger.Printf("Sending message: %s", string(data))
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	_, err = t.writer.Write(data)
	return err
}

// SetCloseHandler sets the handler for close events
func (t *StdioServerTransport) SetCloseHandler(handler func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onClose = handler
}

// SetErrorHandler sets the handler for error events
func (t *StdioServerTransport) SetErrorHandler(handler func(error)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onError = handler
}

// SetMessageHandler sets the handler for incoming messages
func (t *StdioServerTransport) SetMessageHandler(handler func(ctx context.Context, message *transport.BaseJsonRpcMessage)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onMessage = handler
}

func (t *StdioServerTransport) readLoop(ctx context.Context) {
	buffer := make([]byte, 4096)
	for {
		select {
		case <-ctx.Done():
			t.Close()
			return
		default:
			t.mu.Lock()
			if !t.started {
				t.mu.Unlock()
				return
			}
			t.mu.Unlock()

			n, err := t.reader.Read(buffer)
			if err != nil {
				if err != io.EOF {
					t.handleError(fmt.Errorf("read error: %w", err))
				}
				return
			}

			if t.logger != nil {
				t.logger.Printf("Read %d bytes from stdin", n)
			}

			t.readBuf.Append(buffer[:n])
			t.processReadBuffer()
		}
	}
}

func (t *StdioServerTransport) processReadBuffer() {
	for {
		msg, err := t.readBuf.ReadMessage()
		if err != nil {
			//println("error reading message:", err.Error())
			if t.logger != nil {
				t.logger.Printf("Error reading message: %v", err)
			}
			t.handleError(err)
			return
		}
		if msg == nil {
			//println("no message")
			return
		}
		//println("received message:", spew.Sprint(msg))
		if t.logger != nil && msg != nil {
			t.logger.Printf("Received message: %+v", msg)
		}
		t.handleMessage(msg)
	}
}

func (t *StdioServerTransport) handleError(err error) {
	if t.logger != nil {
		t.logger.Printf("Transport error: %v", err)
	}

	t.mu.Lock()
	handler := t.onError
	t.mu.Unlock()

	if handler != nil {
		handler(err)
	}
}

func (t *StdioServerTransport) handleMessage(msg *transport.BaseJsonRpcMessage) {
	t.mu.Lock()
	handler := t.onMessage
	t.mu.Unlock()

	ctx := context.Background()

	if handler != nil {
		handler(ctx, msg)
	}
}
