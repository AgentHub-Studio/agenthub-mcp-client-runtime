package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// Protocol handles JSON-RPC 2.0 communication over stdio
type Protocol struct {
	reader      *bufio.Reader
	writer      io.Writer
	writeMutex  sync.Mutex
	pendingReqs sync.Map // map[interface{}]chan *JSONRPCResponse
	requestID   atomic.Int64
}

// NewProtocol creates a new protocol handler
func NewProtocol(reader io.Reader, writer io.Writer) *Protocol {
	return &Protocol{
		reader: bufio.NewReader(reader),
		writer: writer,
	}
}

// NextRequestID generates a unique request ID
func (p *Protocol) NextRequestID() int64 {
	return p.requestID.Add(1)
}

// SendRequest sends a JSON-RPC request and waits for response
func (p *Protocol) SendRequest(method string, params interface{}) (*JSONRPCResponse, error) {
	id := p.NextRequestID()
	
	req, err := NewJSONRPCRequest(id, method, params)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Create response channel
	responseChan := make(chan *JSONRPCResponse, 1)
	p.pendingReqs.Store(id, responseChan)
	defer p.pendingReqs.Delete(id)

	// Send request
	if err := p.writeMessage(req); err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	// Wait for response
	response := <-responseChan
	return response, nil
}

// SendNotification sends a JSON-RPC notification (no response expected)
func (p *Protocol) SendNotification(method string, params interface{}) error {
	req, err := NewJSONRPCRequest(nil, method, params)
	if err != nil {
		return fmt.Errorf("failed to create notification: %w", err)
	}

	return p.writeMessage(req)
}

// ReadMessages reads and handles incoming JSON-RPC messages
func (p *Protocol) ReadMessages() error {
	for {
		line, err := p.reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("failed to read message: %w", err)
		}

		if len(line) == 0 {
			continue
		}

		// Try to parse as response first
		var response JSONRPCResponse
		if err := json.Unmarshal(line, &response); err == nil && response.ID != nil {
			p.handleResponse(&response)
			continue
		}

		// Try to parse as request (server-initiated)
		var request JSONRPCRequest
		if err := json.Unmarshal(line, &request); err == nil {
			p.handleRequest(&request)
			continue
		}

		// Unknown message format
		fmt.Printf("Warning: unable to parse message: %s\n", string(line))
	}
}

// handleResponse routes response to waiting request
func (p *Protocol) handleResponse(response *JSONRPCResponse) {
	if response.ID == nil {
		return
	}

	// Convert ID to comparable type
	var id interface{}
	switch v := response.ID.(type) {
	case float64:
		id = int64(v)
	default:
		id = v
	}

	if ch, ok := p.pendingReqs.Load(id); ok {
		ch.(chan *JSONRPCResponse) <- response
	}
}

// handleRequest handles server-initiated requests (notifications)
func (p *Protocol) handleRequest(request *JSONRPCRequest) {
	// For now, just log server-initiated requests
	// In a full implementation, we'd have handlers for these
	fmt.Printf("Received server request: method=%s\n", request.Method)
}

// writeMessage writes a JSON-RPC message to stdout
func (p *Protocol) writeMessage(message interface{}) error {
	p.writeMutex.Lock()
	defer p.writeMutex.Unlock()

	data, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	// Write message followed by newline
	data = append(data, '\n')
	
	if _, err := p.writer.Write(data); err != nil {
		return fmt.Errorf("failed to write message: %w", err)
	}

	return nil
}
