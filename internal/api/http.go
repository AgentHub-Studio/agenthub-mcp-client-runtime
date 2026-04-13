package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/agenthub/mcp-client-runtime/internal/mcp"
	"github.com/agenthub/mcp-client-runtime/internal/oauth"
	"github.com/gin-gonic/gin"
)

// HTTPServer provides REST API for MCP client
type HTTPServer struct {
	router      *gin.Engine
	mcpManager  *mcp.Manager
	port        int
	// refresher, when set, is called by handleListAllTools to pick up any MCP
	// server configs that were created after the runtime started.
	// BUG-MCP-RUNTIME-STALE fix: without this, servers created post-startup are
	// never registered in the in-memory manager and therefore invisible to agents.
	refresher ConfigRefresher
}

// ConfigRefresher is called before aggregating tools to ensure any newly-created
// MCP server configs are registered in the in-memory manager.
// Implementations must be idempotent and rate-limit themselves internally.
type ConfigRefresher interface {
	Refresh(ctx context.Context)
}

// WithRefresher sets the config refresher used by handleListAllTools.
// Must be called before the server starts serving requests.
func (s *HTTPServer) WithRefresher(r ConfigRefresher) {
	s.refresher = r
}

// NewHTTPServer creates a new HTTP server
func NewHTTPServer(port int, mcpManager *mcp.Manager) *HTTPServer {
	gin.SetMode(gin.ReleaseMode)
	router := gin.Default()

	server := &HTTPServer{
		router:     router,
		mcpManager: mcpManager,
		port:       port,
	}

	server.setupRoutes()
	return server
}

// setupRoutes configures HTTP routes
func (s *HTTPServer) setupRoutes() {
	// Health check
	s.router.GET("/health", s.handleHealth)

	// Server management
	s.router.GET("/servers", s.handleListServers)
	s.router.GET("/servers/:name/status", s.handleGetServerStatus)
	s.router.POST("/servers", s.handleRegisterServer)
	s.router.DELETE("/servers/:name", s.handleUnregisterServer)
	s.router.POST("/servers/:name/start", s.handleStartServer)
	s.router.POST("/servers/:name/stop", s.handleStopServer)

	// Per-server discovery
	s.router.GET("/servers/:name/tools", s.handleListTools)
	s.router.POST("/servers/:name/tools/:tool/call", s.handleExecuteTool)
	s.router.GET("/servers/:name/prompts", s.handleListPrompts)
	s.router.GET("/servers/:name/resources", s.handleListResources)

	// Registration (DCR)
	s.router.POST("/servers/:name/register-client", s.handleRegisterDynamicClient)

	// Aggregated API — used by agenthub-api MCPToolBridge (P-C253-1).
	// GET  /api/tools             — list all tools from all running servers
	// POST /api/tools/call        — invoke a tool on a named server
	s.router.GET("/api/tools", s.handleListAllTools)
	s.router.POST("/api/tools/call", s.handleCallAggregatedTool)
}

// Start starts the HTTP server
func (s *HTTPServer) Start() error {
	addr := fmt.Sprintf(":%d", s.port)
	fmt.Printf("HTTP server listening on %s\n", addr)
	return s.router.Run(addr)
}

// ========== Handlers ==========

func (s *HTTPServer) handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "healthy",
		"service": "mcp-client-runtime",
		"version": "1.0.0",
	})
}

func (s *HTTPServer) handleListServers(c *gin.Context) {
	servers := s.mcpManager.ListServers()
	c.JSON(http.StatusOK, gin.H{
		"servers": servers,
	})
}

func (s *HTTPServer) handleGetServerStatus(c *gin.Context) {
	name := c.Param("name")
	servers := s.mcpManager.ListServers()
	for _, srv := range servers {
		if srv.Name == name {
			c.JSON(http.StatusOK, srv)
			return
		}
	}
	c.JSON(http.StatusNotFound, gin.H{"error": "server not found"})
}

// RegisterServerRequest holds the payload for registering an MCP server.
// Supports both stdio transport (Command) and Streamable HTTP transport (TransportType + HTTPBaseURL).
type RegisterServerRequest struct {
	Name      string `json:"name" binding:"required"`
	AutoStart bool   `json:"autoStart"`

	// Stdio transport (default when TransportType is empty or "stdio")
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`

	// HTTP transport (TransportType = "http")
	TransportType string `json:"transportType"` // "stdio" or "http"
	HTTPBaseURL   string `json:"httpBaseUrl"`

	// OAuth 2.1 (for protected HTTP servers)
	OAuthTokenURL     string   `json:"oauthTokenUrl"`
	OAuthClientID     string   `json:"oauthClientId"`
	OAuthClientSecret string   `json:"oauthClientSecret"`
	OAuthScopes       []string `json:"oauthScopes"`
	OAuthBearerToken  string   `json:"oauthBearerToken"`
	OAuthRefreshToken string   `json:"oauthRefreshToken"`
}

func (s *HTTPServer) handleRegisterServer(c *gin.Context) {
	var req RegisterServerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Convert env map to slice
	env := make([]string, 0, len(req.Env))
	for k, v := range req.Env {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}

	config := mcp.ClientConfig{
		Name:          req.Name,
		Command:       req.Command,
		Args:          req.Args,
		Env:           env,
		TransportType: req.TransportType,
		HTTPBaseURL:   req.HTTPBaseURL,
	}

	// Build auth provider for HTTP transport.
	// Three modes:
	//   1. Static bearer token (PAT) — OAuthBearerToken set, no OAuthTokenURL
	//   2. Authorization Code flow — OAuthBearerToken + OAuthTokenURL (refresh via token endpoint)
	//   3. Client Credentials flow — OAuthTokenURL + OAuthClientID (machine-to-machine)
	if req.TransportType == "http" {
		if req.OAuthBearerToken != "" && req.OAuthTokenURL == "" {
			// Mode 1: Static token (e.g. GitHub PAT) — never expires, no refresh.
			config.OAuthProvider = oauth.NewStaticTokenProvider(req.OAuthBearerToken)
		} else if req.OAuthTokenURL != "" {
			var tokenClient *oauth.TokenClient
			var err error
			if req.OAuthBearerToken != "" {
				// Mode 2: Authorization Code flow with refresh token.
				tokenClient = oauth.NewAuthCodeTokenClient(
					context.Background(),
					req.OAuthTokenURL,
					req.OAuthClientID,
					req.OAuthClientSecret,
					req.OAuthBearerToken,
					req.OAuthRefreshToken,
				)
			} else {
				// Mode 3: Client Credentials flow.
				tokenClient, err = oauth.NewTokenClient(
					context.Background(),
					req.OAuthTokenURL,
					req.OAuthClientID,
					req.OAuthClientSecret,
					req.OAuthScopes,
				)
			}
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("erro ao configurar OAuth: %v", err)})
				return
			}
			config.OAuthProvider = tokenClient
		}
	}

	if err := s.mcpManager.RegisterServer(config); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// For HTTP transport, we should trigger a discovery check immediately if it's auto-started
	// or even if it's not, to have metadata ready for the "Connect" button in the UI.
	if req.TransportType == "http" {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			// Starting the server in HTTP transport just initializes the client and potentially
			// triggers the 401 discovery if we do a trial request.
			_ = s.mcpManager.StartServer(ctx, req.Name)
		}()
	} else if req.AutoStart {
		// Auto-start for stdio
		if err := s.mcpManager.StartServer(context.Background(), req.Name); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   fmt.Sprintf("registered but failed to start: %v", err),
				"name":    req.Name,
				"started": false,
			})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "server registered successfully",
		"name":    req.Name,
		"started": req.AutoStart,
	})
}

func (s *HTTPServer) handleStartServer(c *gin.Context) {
	name := c.Param("name")

	if err := s.mcpManager.StartServer(context.Background(), name); err != nil {
		if strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "auth") {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":         fmt.Sprintf("failed to start server: %v", err),
				"auth_required": true,
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "server started successfully",
		"name":    name,
	})
}

func (s *HTTPServer) handleStopServer(c *gin.Context) {
	name := c.Param("name")

	if err := s.mcpManager.StopServer(name); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "server stopped successfully",
		"name":    name,
	})
}

func (s *HTTPServer) handleUnregisterServer(c *gin.Context) {
	name := c.Param("name")
	if err := s.mcpManager.UnregisterServer(name); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"message": "server unregistered successfully",
		"name":    name,
	})
}

func (s *HTTPServer) handleListTools(c *gin.Context) {
	name := c.Param("name")

	client, err := s.mcpManager.GetClient(name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	// Try to start if not running (handshake)
	if !client.IsRunning() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := client.Start(ctx)
		cancel()
		if err != nil {
			if isAuthError(err) {
				c.JSON(http.StatusUnauthorized, gin.H{
					"error":         fmt.Sprintf("failed to start client: %v", err),
					"auth_required": true,
				})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to start client: %v", err)})
			return
		}
	}

	result, err := client.ListTools(context.Background())
	if err != nil {
		if isAuthError(err) {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":         err.Error(),
				"auth_required": true,
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"tools": result.Tools,
	})
}

func (s *HTTPServer) handleListPrompts(c *gin.Context) {
	name := c.Param("name")

	client, err := s.mcpManager.GetClient(name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	// Try to start if not running (handshake)
	if !client.IsRunning() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := client.Start(ctx)
		cancel()
		if err != nil {
			if isAuthError(err) {
				c.JSON(http.StatusUnauthorized, gin.H{
					"error":         fmt.Sprintf("failed to start client: %v", err),
					"auth_required": true,
				})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to start client: %v", err)})
			return
		}
	}

	result, err := client.ListPrompts(context.Background())
	if err != nil {
		if isAuthError(err) {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":         err.Error(),
				"auth_required": true,
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"prompts": result.Prompts,
	})
}

func (s *HTTPServer) handleListResources(c *gin.Context) {
	name := c.Param("name")

	client, err := s.mcpManager.GetClient(name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	// Try to start if not running (handshake)
	if !client.IsRunning() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := client.Start(ctx)
		cancel()
		if err != nil {
			if isAuthError(err) {
				c.JSON(http.StatusUnauthorized, gin.H{
					"error":         fmt.Sprintf("failed to start client: %v", err),
					"auth_required": true,
				})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to start client: %v", err)})
			return
		}
	}

	result, err := client.ListResources(context.Background())
	if err != nil {
		if isAuthError(err) {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":         err.Error(),
				"auth_required": true,
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"resources": result.Resources,
	})
}

// isAuthError returns true if the error is an OAuth authentication failure (401).
func isAuthError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "authentication failed (401)") ||
		strings.Contains(msg, "OAuth access token invalid or expired")
}

type ExecuteToolRequest struct {
	Arguments map[string]interface{} `json:"arguments"`
}

func (s *HTTPServer) handleExecuteTool(c *gin.Context) {
	serverName := c.Param("name")
	toolName := c.Param("tool")

	var req ExecuteToolRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	client, err := s.mcpManager.GetClient(serverName)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	if !client.IsRunning() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := client.Start(ctx)
		cancel()
		if err != nil {
			if isAuthError(err) {
				c.JSON(http.StatusUnauthorized, gin.H{
					"error":         fmt.Sprintf("failed to start client: %v", err),
					"auth_required": true,
				})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to start client: %v", err)})
			return
		}
	}

	result, err := client.CallTool(context.Background(), toolName, req.Arguments)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"result":  result.Content,
		"isError": result.IsError,
	})
}

func (s *HTTPServer) handleRegisterDynamicClient(c *gin.Context) {
	name := c.Param("name")

	var req oauth.DynamicClientRegistrationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	client, err := s.mcpManager.GetClient(name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	httpClient, ok := client.(*mcp.HTTPClient)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "not an HTTP client"})
		return
	}

	metadata := httpClient.GetAuthMetadata()
	if metadata == nil || metadata.RegistrationURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "server does not support dynamic registration (no registration_url found)"})
		return
	}

	resp, err := oauth.RegisterDynamicClient(c.Request.Context(), metadata.RegistrationURL, req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	httpClient.SetDynamicClientID(resp.ClientID)

	c.JSON(http.StatusOK, resp)
}

// ========== Aggregated API (P-C253-1) ==========

// aggregatedTool mirrors MCPToolInfo in agenthub-api — must stay in sync.
type aggregatedTool struct {
	ServerName  string      `json:"serverName"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"inputSchema"`
}

// handleListAllTools aggregates tools from every running MCP server so that
// agenthub-api can call GET /api/tools?tenantId=<id> to discover all tools at once.
// BUG-MCP2: failures to start or list tools are included in the "warnings" array
// so the caller can surface them rather than silently receiving a partial tools list.
func (s *HTTPServer) handleListAllTools(c *gin.Context) {
	// BUG-MCP-RUNTIME-STALE fix: refresh registered servers from backend before
	// listing tools so that MCPs created after startup become visible to agents.
	if s.refresher != nil {
		s.refresher.Refresh(c.Request.Context())
	}
	servers := s.mcpManager.ListServers()

	var allTools []aggregatedTool
	var warnings []string
	for _, srv := range servers {
		client, err := s.mcpManager.GetClient(srv.Name)
		if err != nil {
			msg := fmt.Sprintf("MCP server %q: not registered (%v)", srv.Name, err)
			log.Printf("handleListAllTools: %s", msg)
			warnings = append(warnings, msg)
			continue
		}

		// Auto-start if not yet running so the first request triggers the handshake.
		if !client.IsRunning() {
			ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
			startErr := client.Start(ctx)
			cancel()
			if startErr != nil {
				msg := fmt.Sprintf("MCP server %q: failed to start (%v)", srv.Name, startErr)
				log.Printf("handleListAllTools: %s", msg)
				warnings = append(warnings, msg)
				continue
			}
		}

		if !client.IsRunning() {
			msg := fmt.Sprintf("MCP server %q: not running after start attempt", srv.Name)
			log.Printf("handleListAllTools: %s", msg)
			warnings = append(warnings, msg)
			continue
		}

		result, err := client.ListTools(c.Request.Context())
		if err != nil {
			msg := fmt.Sprintf("MCP server %q: failed to list tools (%v)", srv.Name, err)
			log.Printf("handleListAllTools: %s", msg)
			warnings = append(warnings, msg)
			continue
		}

		for _, tool := range result.Tools {
			allTools = append(allTools, aggregatedTool{
				ServerName:  srv.Name,
				Name:        tool.Name,
				Description: tool.Description,
				InputSchema: tool.InputSchema,
			})
		}
	}

	if allTools == nil {
		allTools = []aggregatedTool{}
	}
	if warnings == nil {
		warnings = []string{}
	}

	c.JSON(http.StatusOK, gin.H{"tools": allTools, "warnings": warnings})
}

// callAggregatedRequest mirrors callToolRequest in agenthub-api's HTTPMCPClient.
type callAggregatedRequest struct {
	ServerName string          `json:"serverName" binding:"required"`
	ToolName   string          `json:"toolName"   binding:"required"`
	Input      json.RawMessage `json:"input"`
}

// handleCallAggregatedTool routes POST /api/tools/call to the correct MCP server.
// Response: {"output": <content>, "isError": bool}
func (s *HTTPServer) handleCallAggregatedTool(c *gin.Context) {
	var req callAggregatedRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	client, err := s.mcpManager.GetClient(req.ServerName)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("server %q not found", req.ServerName)})
		return
	}

	if !client.IsRunning() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		startErr := client.Start(ctx)
		cancel()
		if startErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to start server: %v", startErr)})
			return
		}
	}

	// Unmarshal input JSON into map for the MCP CallTool interface.
	var arguments map[string]interface{}
	if len(req.Input) > 0 && string(req.Input) != "null" {
		if err := json.Unmarshal(req.Input, &arguments); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid input JSON: %v", err)})
			return
		}
	}

	result, err := client.CallTool(c.Request.Context(), req.ToolName, arguments)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"output":  result.Content,
		"isError": result.IsError,
	})
}
