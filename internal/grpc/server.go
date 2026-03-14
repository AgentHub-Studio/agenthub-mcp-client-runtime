package grpc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/agenthub/mcp-client-runtime/internal/mcp"
	"google.golang.org/grpc"
)

// Server implements the gRPC MCPClientService
type Server struct {
	grpcServer *grpc.Server
	mcpManager *mcp.Manager
	port       int
}

// NewServer creates a new gRPC server
func NewServer(port int, mcpManager *mcp.Manager) *Server {
	return &Server{
		grpcServer: grpc.NewServer(),
		mcpManager: mcpManager,
		port:       port,
	}
}

// Start starts the gRPC server
func (s *Server) Start() error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", s.port))
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}

	// Register service (manual implementation since we don't have generated code yet)
	// In production, this would be: pb.RegisterMCPClientServiceServer(s.grpcServer, s)

	fmt.Printf("gRPC server listening on :%d\n", s.port)
	return s.grpcServer.Serve(lis)
}

// Stop stops the gRPC server
func (s *Server) Stop() {
	s.grpcServer.GracefulStop()
}

// ========== gRPC Service Implementation ==========
// These methods will be called by the generated protobuf code

// DiscoverCapabilities discovers capabilities of an MCP server
func (s *Server) DiscoverCapabilities(ctx context.Context, serverName string) (*DiscoverResponse, error) {
	client, err := s.mcpManager.GetClient(serverName)
	if err != nil {
		return nil, fmt.Errorf("server not found: %w", err)
	}

	// Ensure server is running
	if !client.IsRunning() {
		if err := s.mcpManager.StartServer(ctx, serverName); err != nil {
			return nil, fmt.Errorf("failed to start server: %w", err)
		}
	}

	// Get tools
	toolsResult, err := client.ListTools(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}

	// Get prompts
	promptsResult, err := client.ListPrompts(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list prompts: %w", err)
	}

	// Get resources
	resourcesResult, err := client.ListResources(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list resources: %w", err)
	}

	// Get server info
	serverInfo := client.GetServerInfo()

	return &DiscoverResponse{
		Tools:      convertTools(toolsResult.Tools),
		Prompts:    convertPrompts(promptsResult.Prompts),
		Resources:  convertResources(resourcesResult.Resources),
		ServerInfo: convertServerInfo(serverInfo),
	}, nil
}

// ExecuteTool executes a tool on an MCP server
func (s *Server) ExecuteTool(ctx context.Context, req *ExecuteToolRequest) (*ExecuteToolResponse, error) {
	startTime := time.Now()

	client, err := s.mcpManager.GetClient(req.ServerName)
	if err != nil {
		return &ExecuteToolResponse{
			Success: false,
			Error:   fmt.Sprintf("server not found: %v", err),
		}, nil
	}

	// Parse parameters
	var params map[string]interface{}
	if req.Parameters != nil {
		params = make(map[string]interface{})
		for k, v := range req.Parameters {
			var value interface{}
			if err := json.Unmarshal([]byte(v), &value); err != nil {
				params[k] = v // Use string as fallback
			} else {
				params[k] = value
			}
		}
	}

	// Execute tool
	result, err := client.CallTool(ctx, req.ToolName, params)
	if err != nil {
		return &ExecuteToolResponse{
			Success:    false,
			Error:      err.Error(),
			DurationMs: int32(time.Since(startTime).Milliseconds()),
		}, nil
	}

	// Convert result to JSON
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return &ExecuteToolResponse{
			Success:    false,
			Error:      fmt.Sprintf("failed to marshal result: %v", err),
			DurationMs: int32(time.Since(startTime).Milliseconds()),
		}, nil
	}

	return &ExecuteToolResponse{
		Success:    !result.IsError,
		Result:     string(resultJSON),
		Error:      "",
		DurationMs: int32(time.Since(startTime).Milliseconds()),
	}, nil
}

// ExecutePrompt executes a prompt on an MCP server
func (s *Server) ExecutePrompt(ctx context.Context, req *ExecutePromptRequest) (*ExecutePromptResponse, error) {
	startTime := time.Now()

	client, err := s.mcpManager.GetClient(req.ServerName)
	if err != nil {
		return &ExecutePromptResponse{
			Success: false,
			Error:   fmt.Sprintf("server not found: %v", err),
		}, nil
	}

	// Parse arguments
	var args map[string]interface{}
	if req.Arguments != nil {
		args = make(map[string]interface{})
		for k, v := range req.Arguments {
			var value interface{}
			if err := json.Unmarshal([]byte(v), &value); err != nil {
				args[k] = v
			} else {
				args[k] = value
			}
		}
	}

	// Execute prompt
	result, err := client.GetPrompt(ctx, req.PromptName, args)
	if err != nil {
		return &ExecutePromptResponse{
			Success:    false,
			Error:      err.Error(),
			DurationMs: int32(time.Since(startTime).Milliseconds()),
		}, nil
	}

	// Convert result to JSON
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return &ExecutePromptResponse{
			Success:    false,
			Error:      fmt.Sprintf("failed to marshal result: %v", err),
			DurationMs: int32(time.Since(startTime).Milliseconds()),
		}, nil
	}

	return &ExecutePromptResponse{
		Success:    true,
		Result:     string(resultJSON),
		DurationMs: int32(time.Since(startTime).Milliseconds()),
	}, nil
}

// ReadResource reads a resource from an MCP server
func (s *Server) ReadResource(ctx context.Context, req *ReadResourceRequest) (*ReadResourceResponse, error) {
	client, err := s.mcpManager.GetClient(req.ServerName)
	if err != nil {
		return &ReadResourceResponse{
			Success: false,
			Error:   fmt.Sprintf("server not found: %v", err),
		}, nil
	}

	result, err := client.ReadResource(ctx, req.ResourceUri)
	if err != nil {
		return &ReadResourceResponse{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	// Combine all contents into a single response
	if len(result.Contents) > 0 {
		content := result.Contents[0]
		var text string
		if content.Text != "" {
			text = content.Text
		} else if content.Blob != "" {
			text = content.Blob
		}

		return &ReadResourceResponse{
			Success:   true,
			Content:   text,
			MimeType:  content.MimeType,
			SizeBytes: int32(len(text)),
		}, nil
	}

	return &ReadResourceResponse{
		Success: false,
		Error:   "no content returned",
	}, nil
}

// ListServers lists all registered MCP servers
func (s *Server) ListServers(ctx context.Context) (*ListServersResponse, error) {
	servers := s.mcpManager.ListServers()

	response := &ListServersResponse{
		Servers: make([]*MCPServer, len(servers)),
	}

	for i, server := range servers {
		response.Servers[i] = &MCPServer{
			Name:      server.Name,
			Command:   server.Command,
			Args:      server.Args,
			Status:    server.Status,
			StartedAt: server.StartedAt.Unix(),
		}
	}

	return response, nil
}

// RegisterServer registers a new MCP server
func (s *Server) RegisterServer(ctx context.Context, req *RegisterServerRequest) (*RegisterServerResponse, error) {
	config := mcp.ClientConfig{
		Name:    req.Name,
		Command: req.Command,
		Args:    req.Args,
		Env:     mapToEnvArray(req.Env),
	}

	if err := s.mcpManager.RegisterServer(config); err != nil {
		return &RegisterServerResponse{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	// Auto-start if requested
	if req.AutoStart {
		if err := s.mcpManager.StartServer(ctx, req.Name); err != nil {
			return &RegisterServerResponse{
				Success: false,
				Error:   fmt.Sprintf("registered but failed to start: %v", err),
			}, nil
		}
	}

	return &RegisterServerResponse{
		Success: true,
		Server: &MCPServer{
			Name:    req.Name,
			Command: req.Command,
			Args:    req.Args,
			Env:     req.Env,
			Status:  "stopped",
		},
	}, nil
}

// ========== Helper Functions ==========

func convertTools(tools []mcp.Tool) []*Tool {
	result := make([]*Tool, len(tools))
	for i, t := range tools {
		// Convert input schema to string
		schemaJSON, _ := json.Marshal(t.InputSchema)
		params := make(map[string]string)
		params["schema"] = string(schemaJSON)

		result[i] = &Tool{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  params,
		}
	}
	return result
}

func convertPrompts(prompts []mcp.Prompt) []*Prompt {
	result := make([]*Prompt, len(prompts))
	for i, p := range prompts {
		args := make([]*PromptArgument, len(p.Arguments))
		for j, a := range p.Arguments {
			args[j] = &PromptArgument{
				Name:        a.Name,
				Description: a.Description,
				Required:    a.Required,
			}
		}

		result[i] = &Prompt{
			Name:        p.Name,
			Description: p.Description,
			Arguments:   args,
		}
	}
	return result
}

func convertResources(resources []mcp.Resource) []*Resource {
	result := make([]*Resource, len(resources))
	for i, r := range resources {
		result[i] = &Resource{
			Uri:         r.URI,
			Name:        r.Name,
			Description: r.Description,
			MimeType:    r.MimeType,
		}
	}
	return result
}

func convertServerInfo(info *mcp.ServerInfo) *ServerInfo {
	if info == nil {
		return nil
	}
	return &ServerInfo{
		Name:            info.Name,
		Version:         info.Version,
		ProtocolVersion: info.ProtocolVersion,
	}
}

func mapToEnvArray(envMap map[string]string) []string {
	if envMap == nil {
		return nil
	}

	env := make([]string, 0, len(envMap))
	for k, v := range envMap {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	return env
}
