package http

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/metoro-io/mcp-golang/transport"
)

// baseTransport implements the common functionality for HTTP-based transports
type baseTransport struct {
	messageHandler func(ctx context.Context, message *transport.BaseJsonRpcMessage)
	errorHandler   func(error)
	closeHandler   func()
	mu             sync.RWMutex
	responseMap    map[string]chan *transport.BaseJsonRpcMessage
}

func newBaseTransport() *baseTransport {
	return &baseTransport{
		responseMap: make(map[string]chan *transport.BaseJsonRpcMessage),
	}
}

// Send implements Transport.Send
func (t *baseTransport) Send(ctx context.Context, message *transport.BaseJsonRpcMessage) error {
	key := message.JsonRpcResponse.Id.String()
	responseChannel := t.responseMap[key]
	if responseChannel == nil {
		return fmt.Errorf("no response channel found for key: %s", key)
	}
	responseChannel <- message
	return nil
}

// Close implements Transport.Close
func (t *baseTransport) Close() error {
	if t.closeHandler != nil {
		t.closeHandler()
	}
	return nil
}

// SetCloseHandler implements Transport.SetCloseHandler
func (t *baseTransport) SetCloseHandler(handler func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closeHandler = handler
}

// SetErrorHandler implements Transport.SetErrorHandler
func (t *baseTransport) SetErrorHandler(handler func(error)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.errorHandler = handler
}

// SetMessageHandler implements Transport.SetMessageHandler
func (t *baseTransport) SetMessageHandler(handler func(ctx context.Context, message *transport.BaseJsonRpcMessage)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.messageHandler = handler
}

// handleMessage processes an incoming message and returns a response
func (t *baseTransport) handleMessage(ctx context.Context, body []byte) (*transport.BaseJsonRpcMessage, error) {
	// Store the response writer for later use
	t.mu.Lock()
	var keyStr string = "0"

	for {
		if _, ok := t.responseMap[keyStr]; !ok {
			break
		}
		// Increment the key as a string
		keyInt := 0
		fmt.Sscanf(keyStr, "%d", &keyInt)
		keyInt++
		keyStr = fmt.Sprintf("%d", keyInt)
	}
	t.responseMap[keyStr] = make(chan *transport.BaseJsonRpcMessage)
	t.mu.Unlock()

	var prevId *transport.RequestId = nil
	deserialized := false
	// Try to unmarshal as a request first
	var request transport.BaseJSONRPCRequest
	if err := json.Unmarshal(body, &request); err == nil {
		deserialized = true
		id := request.Id
		prevId = &id
		request.Id = transport.RequestId{
			StringValue: keyStr,
			IsString:    true,
		}
		t.mu.RLock()
		handler := t.messageHandler
		t.mu.RUnlock()

		if handler != nil {
			handler(ctx, transport.NewBaseMessageRequest(&request))
		}
	}

	// Try as a notification
	var notification transport.BaseJSONRPCNotification
	if !deserialized {
		if err := json.Unmarshal(body, &notification); err == nil {
			deserialized = true
			t.mu.RLock()
			handler := t.messageHandler
			t.mu.RUnlock()

			if handler != nil {
				handler(ctx, transport.NewBaseMessageNotification(&notification))
			}
		}
	}

	// Try as a response
	var response transport.BaseJSONRPCResponse
	if !deserialized {
		if err := json.Unmarshal(body, &response); err == nil {
			deserialized = true
			t.mu.RLock()
			handler := t.messageHandler
			t.mu.RUnlock()

			if handler != nil {
				handler(ctx, transport.NewBaseMessageResponse(&response))
			}
		}
	}

	// Try as an error
	var errorResponse transport.BaseJSONRPCError
	if !deserialized {
		if err := json.Unmarshal(body, &errorResponse); err == nil {
			deserialized = true
			t.mu.RLock()
			handler := t.messageHandler
			t.mu.RUnlock()

			if handler != nil {
				handler(ctx, transport.NewBaseMessageError(&errorResponse))
			}
		}
	}

	// Block until the response is received
	responseToUse := <-t.responseMap[keyStr]
	delete(t.responseMap, keyStr)
	if prevId != nil {
		responseToUse.JsonRpcResponse.Id = *prevId
	}

	return responseToUse, nil
}

// readBody reads and returns the body from an io.Reader
func (t *baseTransport) readBody(reader io.Reader) ([]byte, error) {
	body, err := io.ReadAll(reader)
	if err != nil {
		if t.errorHandler != nil {
			t.errorHandler(fmt.Errorf("failed to read request body: %w", err))
		}
		return nil, fmt.Errorf("failed to read request body: %w", err)
	}
	return body, nil
}
