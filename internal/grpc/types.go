package grpc

// Temporary types until protoc generates the real ones
// These simulate what protoc-gen-go would generate

// Request types
type DiscoverRequest struct {
	ServerName string
}

type ExecuteToolRequest struct {
	ServerName string
	ToolName   string
	Parameters map[string]string
	TimeoutMs  int32
}

type ExecutePromptRequest struct {
	ServerName string
	PromptName string
	Arguments  map[string]string
	TimeoutMs  int32
}

type ReadResourceRequest struct {
	ServerName  string
	ResourceUri string
	TimeoutMs   int32
}

type ListServersRequest struct {
	// Empty
}

type RegisterServerRequest struct {
	Name      string
	Command   string
	Args      []string
	Env       map[string]string
	AutoStart bool
}

// Response types
type DiscoverResponse struct {
	Tools      []*Tool
	Prompts    []*Prompt
	Resources  []*Resource
	ServerInfo *ServerInfo
}

type ExecuteToolResponse struct {
	Success    bool
	Result     string
	Error      string
	DurationMs int32
}

type ExecutePromptResponse struct {
	Success    bool
	Result     string
	Error      string
	DurationMs int32
}

type ReadResourceResponse struct {
	Success   bool
	Content   string
	MimeType  string
	Error     string
	SizeBytes int32
}

type ListServersResponse struct {
	Servers []*MCPServer
}

type RegisterServerResponse struct {
	Success bool
	Error   string
	Server  *MCPServer
}

// Domain types
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]string
}

type Prompt struct {
	Name        string
	Description string
	Arguments   []*PromptArgument
}

type PromptArgument struct {
	Name        string
	Description string
	Required    bool
}

type Resource struct {
	Uri         string
	Name        string
	Description string
	MimeType    string
}

type ServerInfo struct {
	Name            string
	Version         string
	ProtocolVersion string
}

type MCPServer struct {
	Name      string
	Command   string
	Args      []string
	Env       map[string]string
	Status    string
	StartedAt int64
}
