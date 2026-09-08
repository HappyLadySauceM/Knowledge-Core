// Command apidocgen generates the checked-in HTTP and WebSocket API documents.
//
// The Thrift HTTP IDL remains the source of truth for paths, methods, and wire
// types. Human-facing descriptions and protocol details live in the adjacent
// metadata files so the IDL stays focused on the transport contract.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/cloudwego/thriftgo/parser"
	"github.com/cloudwego/thriftgo/semantic"
	"gopkg.in/yaml.v3"
)

const (
	openAPIFile   = "openapi.yaml"
	asyncAPIFile  = "asyncapi.yaml"
	openAPIJSON   = "openapi.json"
	asyncAPIJSON  = "asyncapi.json"
	indexHTMLFile = "index.html"
	httpHTMLFile  = "http.html"
	wsHTMLFile    = "websocket.html"
	styleFile     = "style.css"
)

var (
	checkFlag = flag.Bool("check", false, "check generated files without writing them")
	rootFlag  = flag.String("root", ".", "repository root")
)

type metadata struct {
	Version    int                     `yaml:"version"`
	Defaults   metadataDefaults        `yaml:"defaults"`
	Operations map[string]operationDoc `yaml:"operations"`
	Schemas    map[string]schemaDoc    `yaml:"schemas"`
}

type metadataDefaults struct {
	Errors []int `yaml:"errors"`
}

type operationDoc struct {
	Tag            string   `yaml:"tag"`
	Summary        string   `yaml:"summary"`
	Description    string   `yaml:"description"`
	Security       string   `yaml:"security"`
	SuccessStatus  int      `yaml:"success_status"`
	Errors         []int    `yaml:"errors"`
	RequiredBody   []string `yaml:"required_body"`
	ResponseHeader []string `yaml:"response_headers"`
}

type schemaDoc struct {
	Description string                 `yaml:"description"`
	Properties  map[string]propertyDoc `yaml:"properties"`
}

type propertyDoc struct {
	Description string `yaml:"description"`
	Format      string `yaml:"format"`
	Pattern     string `yaml:"pattern"`
	MinLength   *int   `yaml:"min_length"`
	MaxLength   *int   `yaml:"max_length"`
	Minimum     *int64 `yaml:"minimum"`
	Maximum     *int64 `yaml:"maximum"`
}

type operation struct {
	Name         string
	Method       string
	Path         string
	RequestType  string
	ResponseType string
	Doc          operationDoc
}

type document struct {
	OpenAPI  map[string]any
	AsyncAPI map[string]any
	HTML     map[string][]byte
}

func main() {
	flag.Parse()
	root, err := filepath.Abs(*rootFlag)
	if err != nil {
		fatal(err)
	}
	generated, err := generate(root)
	if err != nil {
		fatal(err)
	}
	if err := writeOrCheck(root, generated, *checkFlag); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "apidocgen:", err)
	os.Exit(1)
}

func generate(root string) (document, error) {
	metadataPath := filepath.Join(root, "services", "gateway", "internal", "apidocs", "metadata.yaml")
	wsMetadataPath := filepath.Join(root, "services", "gateway", "internal", "apidocs", "websocket.yaml")
	idlPath := filepath.Join(root, "idl", "http", "v1", "gateway.thrift")
	metadataValue, err := loadMetadata(metadataPath)
	if err != nil {
		return document{}, err
	}
	wsMetadata, err := loadWebSocketMetadata(wsMetadataPath)
	if err != nil {
		return document{}, err
	}
	idl, err := parser.ParseFile(idlPath, []string{filepath.Dir(idlPath)}, false)
	if err != nil {
		return document{}, fmt.Errorf("parse HTTP IDL: %w", err)
	}
	if err := semantic.ResolveSymbols(idl); err != nil {
		return document{}, fmt.Errorf("resolve HTTP IDL symbols: %w", err)
	}
	service := findService(idl, "GatewayService")
	if service == nil {
		return document{}, errors.New("GatewayService is missing from HTTP IDL")
	}
	operations, structs, err := collectOperations(service, idl, metadataValue)
	if err != nil {
		return document{}, err
	}
	openAPI, err := buildOpenAPI(operations, structs, metadataValue)
	if err != nil {
		return document{}, err
	}
	asyncAPI := buildAsyncAPI(wsMetadata)
	html, err := buildHTML(operations, structs, asyncAPI, wsMetadata)
	if err != nil {
		return document{}, err
	}
	return document{OpenAPI: openAPI, AsyncAPI: asyncAPI, HTML: html}, nil
}

func loadMetadata(path string) (metadata, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return metadata{}, fmt.Errorf("read HTTP API metadata %q: %w", path, err)
	}
	var value metadata
	decoder := yaml.NewDecoder(bytes.NewReader(contents))
	decoder.KnownFields(true)
	if err := decoder.Decode(&value); err != nil {
		return metadata{}, fmt.Errorf("decode HTTP API metadata: %w", err)
	}
	if value.Version != 1 {
		return metadata{}, fmt.Errorf("HTTP API metadata version must be 1, got %d", value.Version)
	}
	if len(value.Defaults.Errors) == 0 {
		return metadata{}, errors.New("HTTP API metadata defaults.errors is required")
	}
	if err := validateStatusList("defaults", "errors", value.Defaults.Errors); err != nil {
		return metadata{}, err
	}
	return value, nil
}

type webSocketMetadata struct {
	Version     int                `yaml:"version"`
	Title       string             `yaml:"title"`
	Description string             `yaml:"description"`
	Subprotocol string             `yaml:"subprotocol"`
	Address     string             `yaml:"address"`
	Messages    []webSocketMessage `yaml:"messages"`
	CloseCodes  []webSocketClose   `yaml:"close_codes"`
}

type webSocketMessage struct {
	Name        string `yaml:"name"`
	Direction   string `yaml:"direction"`
	Summary     string `yaml:"summary"`
	Description string `yaml:"description"`
}

type webSocketClose struct {
	Code        int    `yaml:"code"`
	Reason      string `yaml:"reason"`
	Description string `yaml:"description"`
}

func loadWebSocketMetadata(path string) (webSocketMetadata, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return webSocketMetadata{}, fmt.Errorf("read WebSocket metadata %q: %w", path, err)
	}
	var value webSocketMetadata
	decoder := yaml.NewDecoder(bytes.NewReader(contents))
	decoder.KnownFields(true)
	if err := decoder.Decode(&value); err != nil {
		return webSocketMetadata{}, fmt.Errorf("decode WebSocket metadata: %w", err)
	}
	if value.Version != 1 || value.Title == "" || value.Description == "" || value.Subprotocol == "" || value.Address == "" {
		return webSocketMetadata{}, errors.New("WebSocket metadata requires version 1, title, description, subprotocol, and address")
	}
	if len(value.Messages) == 0 || len(value.CloseCodes) == 0 {
		return webSocketMetadata{}, errors.New("WebSocket metadata must define messages and close codes")
	}
	seen := make(map[string]struct{}, len(value.Messages))
	for _, message := range value.Messages {
		if message.Name == "" || (message.Direction != "send" && message.Direction != "receive") {
			return webSocketMetadata{}, fmt.Errorf("invalid WebSocket message %q", message.Name)
		}
		if _, ok := seen[message.Name]; ok {
			return webSocketMetadata{}, fmt.Errorf("duplicate WebSocket message %q", message.Name)
		}
		seen[message.Name] = struct{}{}
	}
	return value, nil
}

func findService(idl *parser.Thrift, name string) *parser.Service {
	for _, service := range idl.Services {
		if service != nil && service.Name == name {
			return service
		}
	}
	return nil
}

func collectOperations(service *parser.Service, idl *parser.Thrift, docs metadata) ([]operation, map[string]*parser.StructLike, error) {
	structs := make(map[string]*parser.StructLike, len(idl.Structs))
	for _, value := range idl.Structs {
		if value != nil {
			if _, exists := structs[value.Name]; exists {
				return nil, nil, fmt.Errorf("duplicate HTTP struct %q", value.Name)
			}
			structs[value.Name] = value
		}
	}
	operations := make([]operation, 0, len(service.Functions))
	seenNames := make(map[string]struct{}, len(service.Functions))
	seenRoutes := make(map[string]struct{}, len(service.Functions))
	for _, function := range service.Functions {
		if function == nil {
			continue
		}
		if _, exists := seenNames[function.Name]; exists {
			return nil, nil, fmt.Errorf("duplicate HTTP operation %q", function.Name)
		}
		seenNames[function.Name] = struct{}{}
		method, path, err := httpAnnotation(function.Annotations)
		if err != nil {
			return nil, nil, fmt.Errorf("operation %s: %w", function.Name, err)
		}
		route := strings.ToUpper(method) + " " + path
		if _, exists := seenRoutes[route]; exists {
			return nil, nil, fmt.Errorf("duplicate HTTP route %s", route)
		}
		seenRoutes[route] = struct{}{}
		doc, ok := docs.Operations[function.Name]
		if !ok {
			return nil, nil, fmt.Errorf("operation %s has no metadata", function.Name)
		}
		if strings.TrimSpace(doc.Tag) == "" || strings.TrimSpace(doc.Summary) == "" || strings.TrimSpace(doc.Description) == "" {
			return nil, nil, fmt.Errorf("operation %s metadata requires tag, summary, and description", function.Name)
		}
		if doc.Security != "none" && doc.Security != "optional" && doc.Security != "bearer" && doc.Security != "admin" {
			return nil, nil, fmt.Errorf("operation %s has invalid security %q", function.Name, doc.Security)
		}
		if doc.SuccessStatus < 100 || doc.SuccessStatus > 599 {
			return nil, nil, fmt.Errorf("operation %s has invalid success status %d", function.Name, doc.SuccessStatus)
		}
		if err := validateStatusList(function.Name, "errors", doc.Errors); err != nil {
			return nil, nil, err
		}
		if err := validateHeaderList(function.Name, doc.ResponseHeader); err != nil {
			return nil, nil, err
		}
		if len(function.Arguments) != 1 || function.Arguments[0] == nil || function.Arguments[0].Type == nil {
			return nil, nil, fmt.Errorf("operation %s must have one typed request argument", function.Name)
		}
		requestType := function.Arguments[0].Type.Name
		responseType := ""
		if function.FunctionType != nil {
			responseType = function.FunctionType.Name
		}
		if responseType == "" {
			return nil, nil, fmt.Errorf("operation %s has no response type", function.Name)
		}
		if _, ok := structs[requestType]; !ok {
			return nil, nil, fmt.Errorf("operation %s request struct %q is missing", function.Name, requestType)
		}
		if _, ok := structs[responseType]; !ok {
			return nil, nil, fmt.Errorf("operation %s response struct %q is missing", function.Name, responseType)
		}
		operations = append(operations, operation{Name: function.Name, Method: strings.ToUpper(method), Path: path, RequestType: requestType, ResponseType: responseType, Doc: doc})
	}
	for name := range docs.Operations {
		if _, ok := seenNames[name]; !ok {
			return nil, nil, fmt.Errorf("metadata contains unknown operation %q", name)
		}
	}
	sort.Slice(operations, func(i, j int) bool { return operations[i].Name < operations[j].Name })
	return operations, structs, nil
}

func httpAnnotation(annotations parser.Annotations) (string, string, error) {
	var method, path string
	for _, annotation := range annotations {
		if annotation == nil || len(annotation.Values) != 1 {
			continue
		}
		switch annotation.Key {
		case "api.get", "api.post", "api.put", "api.patch", "api.delete":
			if method != "" {
				return "", "", errors.New("multiple api HTTP annotations")
			}
			method = strings.TrimPrefix(annotation.Key, "api.")
			path = annotation.Values[0]
		}
	}
	if method == "" || path == "" || !strings.HasPrefix(path, "/") {
		return "", "", errors.New("exactly one valid api HTTP annotation is required")
	}
	return method, path, nil
}

func buildOpenAPI(operations []operation, structs map[string]*parser.StructLike, docs metadata) (map[string]any, error) {
	schemaNames := make([]string, 0, len(structs))
	for name := range structs {
		schemaNames = append(schemaNames, name)
	}
	sort.Strings(schemaNames)
	components := map[string]any{
		"schemas": map[string]any{},
		"securitySchemes": map[string]any{
			"bearerAuth": map[string]any{"type": "http", "scheme": "bearer", "bearerFormat": "JWT"},
		},
	}
	schemaMap := components["schemas"].(map[string]any)
	for _, name := range schemaNames {
		schema, err := structSchema(structs[name], structs, docs)
		if err != nil {
			return nil, fmt.Errorf("schema %s: %w", name, err)
		}
		schemaMap[name] = schema
	}
	paths := map[string]any{}
	tags := make(map[string]struct{})
	for _, operation := range operations {
		// Convert the IDL's :name placeholders to OpenAPI {name} parameters.
		path := colonPathToOpenAPI(operation.Path)
		parameters, requestBody, err := requestParts(operation, structs[operation.RequestType], structs, docs)
		if err != nil {
			return nil, fmt.Errorf("operation %s request: %w", operation.Name, err)
		}
		responses := map[string]any{}
		success := strconv.Itoa(operation.Doc.SuccessStatus)
		response := map[string]any{"description": httpStatusDescription(operation.Doc.SuccessStatus)}
		if operation.Doc.SuccessStatus != 204 && operation.Doc.SuccessStatus != 303 {
			content := map[string]any{"schema": refSchema(operation.ResponseType)}
			if example := exampleFromSchema(refSchema(operation.ResponseType), schemaMap, 0); example != nil {
				content["example"] = example
			}
			response["content"] = map[string]any{"application/json": content}
		}
		responseHeaders := operation.Doc.ResponseHeader
		if len(responseHeaders) == 0 {
			responseHeaders = defaultResponseHeaders(operation)
		}
		if len(responseHeaders) > 0 {
			response["headers"] = responseHeadersSchema(responseHeaders)
		}
		responses[success] = response
		errorsToAdd := operation.Doc.Errors
		if errorsToAdd == nil {
			errorsToAdd = docs.Defaults.Errors
		}
		for _, status := range errorsToAdd {
			if status == operation.Doc.SuccessStatus {
				continue
			}
			key := strconv.Itoa(status)
			errorResponse := map[string]any{
				"description": httpStatusDescription(status),
				"content":     map[string]any{"application/problem+json": map[string]any{"schema": refSchema("HTTPProblem")}},
			}
			if headers := defaultErrorResponseHeaders(status); len(headers) > 0 {
				errorResponse["headers"] = responseHeadersSchema(headers)
			}
			errorContent := errorResponse["content"].(map[string]any)["application/problem+json"].(map[string]any)
			if example := exampleFromSchema(problemSchema(), schemaMap, 0); example != nil {
				if problem, ok := example.(map[string]any); ok {
					problem["status"] = status
					problem["title"] = httpStatusDescription(status)
				}
				errorContent["example"] = example
			}
			responses[key] = errorResponse
		}
		item := map[string]any{
			"operationId": operation.Name,
			"summary":     operation.Doc.Summary,
			"description": operation.Doc.Description,
			"tags":        []string{operation.Doc.Tag},
			"responses":   responses,
		}
		if len(parameters) > 0 {
			item["parameters"] = parameters
		}
		if requestBody != nil {
			content := requestBody["content"].(map[string]any)["application/json"].(map[string]any)
			if example := exampleFromSchema(content["schema"], schemaMap, 0); example != nil {
				content["example"] = example
			}
			item["requestBody"] = requestBody
		}
		switch operation.Doc.Security {
		case "optional":
			item["security"] = []any{map[string]any{}, map[string]any{"bearerAuth": []any{}}}
		case "bearer":
			item["security"] = []any{map[string]any{"bearerAuth": []any{}}}
		case "admin":
			item["security"] = []any{map[string]any{"bearerAuth": []any{}}}
			item["x-required-role"] = "admin"
		default:
			item["security"] = []any{map[string]any{}}
		}
		tags[operation.Doc.Tag] = struct{}{}
		pathItem, ok := paths[path].(map[string]any)
		if !ok {
			pathItem = map[string]any{}
			paths[path] = pathItem
		}
		pathItem[strings.ToLower(operation.Method)] = item
	}
	tagList := make([]string, 0, len(tags))
	for tag := range tags {
		tagList = append(tagList, tag)
	}
	sort.Strings(tagList)
	tagObjects := make([]any, 0, len(tagList))
	for _, tag := range tagList {
		tagObjects = append(tagObjects, map[string]any{"name": tag})
	}
	schemaMap["HTTPProblem"] = problemSchema()
	return map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":       "Knowledge Core Gateway HTTP API",
			"version":     "1.0.0",
			"description": "由 idl/http/v1/gateway.thrift 生成的 Gateway HTTP 契约。成功响应直接返回资源；错误响应使用 RFC 9457 application/problem+json。",
		},
		"servers":    []any{map[string]any{"url": "http://localhost:8080", "description": "本地 Gateway 公网 HTTP 地址；部署环境请替换为实际 public base URL。"}},
		"tags":       tagObjects,
		"paths":      paths,
		"components": components,
	}, nil
}

func colonPathToOpenAPI(path string) string {
	parts := strings.Split(path, "/")
	for index, part := range parts {
		if strings.HasPrefix(part, ":") {
			parts[index] = "{" + strings.TrimPrefix(part, ":") + "}"
		}
	}
	return strings.Join(parts, "/")
}

func problemSchema() map[string]any {
	properties := map[string]any{
		"type":       map[string]any{"type": "string", "format": "uri"},
		"title":      map[string]any{"type": "string"},
		"status":     map[string]any{"type": "integer", "format": "int32"},
		"detail":     map[string]any{"type": "string"},
		"code":       map[string]any{"type": "integer", "format": "int32"},
		"key":        map[string]any{"type": "string"},
		"request_id": map[string]any{"type": "string"},
		"trace_id":   map[string]any{"type": "string"},
	}
	return map[string]any{"type": "object", "required": []string{"type", "title", "status", "detail", "code", "key"}, "properties": properties, "additionalProperties": false}
}

func refSchema(name string) map[string]any {
	return map[string]any{"$ref": "#/components/schemas/" + name}
}

func exampleFromSchema(value any, schemas map[string]any, depth int) any {
	if depth > 5 {
		return nil
	}
	schema, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	if reference, ok := schema["$ref"].(string); ok {
		const prefix = "#/components/schemas/"
		if strings.HasPrefix(reference, prefix) {
			name := strings.TrimPrefix(reference, prefix)
			if resolved, exists := schemas[name]; exists {
				return exampleFromSchema(resolved, schemas, depth+1)
			}
		}
		return nil
	}
	if enum, ok := schema["enum"].([]string); ok && len(enum) > 0 {
		return enum[0]
	}
	if enum, ok := schema["enum"].([]any); ok && len(enum) > 0 {
		return enum[0]
	}
	switch schema["type"] {
	case "object":
		result := map[string]any{}
		properties, _ := schema["properties"].(map[string]any)
		keys := make([]string, 0, len(properties))
		for name := range properties {
			keys = append(keys, name)
		}
		sort.Strings(keys)
		for _, name := range keys {
			if example := exampleFromSchema(properties[name], schemas, depth+1); example != nil {
				result[name] = example
			}
		}
		if len(result) == 0 {
			if additional, ok := schema["additionalProperties"].(map[string]any); ok {
				if example := exampleFromSchema(additional, schemas, depth+1); example != nil {
					result["key"] = example
				}
			}
		}
		return result
	case "array":
		if example := exampleFromSchema(schema["items"], schemas, depth+1); example != nil {
			return []any{example}
		}
		return []any{}
	case "boolean":
		return true
	case "integer":
		return int64(1)
	case "number":
		return 1.0
	case "string":
		switch schema["format"] {
		case "uuid":
			return "018f0c20-7b8a-7cc3-8f89-2f6f6d5f7a11"
		case "date-time":
			return "2026-01-01T00:00:00Z"
		case "email":
			return "user@example.com"
		case "uri":
			return "https://example.com/resource"
		case "byte":
			return "AA=="
		}
		if pattern, ok := schema["pattern"].(string); ok {
			switch pattern {
			case `^[a-z0-9]+(?:-[a-z0-9]+)*$`:
				return "example-document"
			case `^[0-9a-f]{64}$`:
				return strings.Repeat("0", 64)
			}
		}
		return "string"
	}
	return nil
}

func structSchema(value *parser.StructLike, structs map[string]*parser.StructLike, docs metadata) (map[string]any, error) {
	if value == nil {
		return nil, errors.New("nil struct")
	}
	properties := map[string]any{}
	required := []string{}
	propertyDocs := docs.Schemas[value.Name].Properties
	for _, field := range value.Fields {
		if field == nil || field.Type == nil {
			continue
		}
		_, name, err := fieldLocationStrict(field)
		if err != nil {
			return nil, fmt.Errorf("field %s: %w", field.Name, err)
		}
		if name == "" {
			name = field.Name
		}
		property, err := typeSchema(field.Type, structs, docs)
		if err != nil {
			return nil, fmt.Errorf("field %s: %w", field.Name, err)
		}
		if description := propertyDocs[field.Name].Description; description != "" {
			property["description"] = description
		}
		applyPropertyDoc(property, propertyDocs[field.Name])
		applyFieldSemantics(property, name)
		properties[name] = property
		if field.Requiredness == parser.FieldType_Required {
			required = append(required, name)
		}
	}
	sort.Strings(required)
	result := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if description := docs.Schemas[value.Name].Description; description != "" {
		result["description"] = description
	}
	if len(required) > 0 {
		result["required"] = required
	}
	return result, nil
}

func typeSchema(value *parser.Type, structs map[string]*parser.StructLike, docs metadata) (map[string]any, error) {
	if value == nil {
		return nil, errors.New("nil type")
	}
	switch value.Category {
	case parser.Category_Bool:
		return map[string]any{"type": "boolean"}, nil
	case parser.Category_Byte, parser.Category_I16, parser.Category_I32:
		return map[string]any{"type": "integer", "format": "int32"}, nil
	case parser.Category_I64:
		return map[string]any{"type": "integer", "format": "int64"}, nil
	case parser.Category_Double:
		return map[string]any{"type": "number", "format": "double"}, nil
	case parser.Category_String:
		return map[string]any{"type": "string"}, nil
	case parser.Category_Binary:
		return map[string]any{"type": "string", "format": "byte"}, nil
	case parser.Category_List, parser.Category_Set:
		items, err := typeSchema(value.ValueType, structs, docs)
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": "array", "items": items}, nil
	case parser.Category_Map:
		items, err := typeSchema(value.ValueType, structs, docs)
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": "object", "additionalProperties": items}, nil
	case parser.Category_Struct, parser.Category_Union, parser.Category_Exception, parser.Category_Typedef:
		if _, ok := structs[value.Name]; !ok {
			return nil, fmt.Errorf("unknown referenced struct %q", value.Name)
		}
		return refSchema(value.Name), nil
	default:
		if _, ok := structs[value.Name]; ok {
			return refSchema(value.Name), nil
		}
		return nil, fmt.Errorf("unsupported Thrift type %q (%s)", value.Name, value.Category.String())
	}
}

func applyPropertyDoc(property map[string]any, doc propertyDoc) {
	if doc.Format != "" {
		property["format"] = doc.Format
	}
	if doc.Pattern != "" {
		property["pattern"] = doc.Pattern
	}
	if doc.MinLength != nil {
		property["minLength"] = *doc.MinLength
	}
	if doc.MaxLength != nil {
		property["maxLength"] = *doc.MaxLength
	}
	if doc.Minimum != nil {
		property["minimum"] = *doc.Minimum
	}
	if doc.Maximum != nil {
		property["maximum"] = *doc.Maximum
	}
}

func applyFieldSemantics(property map[string]any, name string) {
	if property == nil {
		return
	}
	if _, ok := property["type"].(string); !ok || property["type"] != "string" {
		return
	}
	key := strings.ToLower(strings.ReplaceAll(name, "-", "_"))
	if _, exists := property["format"]; !exists {
		switch {
		case key == "email" || strings.HasSuffix(key, "_email"):
			property["format"] = "email"
		case key == "url" || strings.HasSuffix(key, "_url"):
			property["format"] = "uri"
		case key == "id" || strings.HasSuffix(key, "_id"):
			property["format"] = "uuid"
		case key == "state_vector":
			property["format"] = "byte"
			property["description"] = "Base64url 编码的当前 Yjs state vector。"
		case strings.HasSuffix(key, "_at") || strings.HasSuffix(key, "_date"):
			property["format"] = "date-time"
		}
	}
	if key == "slug" {
		if _, exists := property["pattern"]; !exists {
			property["pattern"] = `^[a-z0-9]+(?:-[a-z0-9]+)*$`
		}
	}
	if key == "sha256" {
		if _, exists := property["pattern"]; !exists {
			property["pattern"] = `^[0-9a-f]{64}$`
		}
		if _, exists := property["minLength"]; !exists {
			property["minLength"] = 64
		}
		if _, exists := property["maxLength"]; !exists {
			property["maxLength"] = 64
		}
	}
}

func validateStatusList(operationName, fieldName string, statuses []int) error {
	seen := make(map[int]struct{}, len(statuses))
	for _, status := range statuses {
		if status < 100 || status > 599 {
			return fmt.Errorf("operation %s has invalid %s status %d", operationName, fieldName, status)
		}
		if _, exists := seen[status]; exists {
			return fmt.Errorf("operation %s has duplicate %s status %d", operationName, fieldName, status)
		}
		seen[status] = struct{}{}
	}
	return nil
}

func validateHeaderList(operationName string, headers []string) error {
	seen := make(map[string]struct{}, len(headers))
	for _, header := range headers {
		name := strings.TrimSpace(header)
		if name == "" {
			return fmt.Errorf("operation %s has an empty response header", operationName)
		}
		key := strings.ToLower(name)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("operation %s has duplicate response header %q", operationName, name)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func requestParts(operation operation, request *parser.StructLike, structs map[string]*parser.StructLike, docs metadata) ([]any, map[string]any, error) {
	if request == nil {
		return nil, nil, errors.New("request struct is nil")
	}
	parameters := []any{}
	bodyProperties := map[string]any{}
	pathNames := pathParameterNames(operation.Path)
	seenPathNames := make(map[string]struct{}, len(pathNames))
	seenParameters := make(map[string]struct{})
	bodyRequired := make(map[string]struct{}, len(operation.Doc.RequiredBody))
	for _, name := range operation.Doc.RequiredBody {
		bodyRequired[name] = struct{}{}
	}
	for _, field := range request.Fields {
		if field == nil || field.Type == nil {
			continue
		}
		location, name, locationErr := fieldLocationStrict(field)
		if locationErr != nil {
			return nil, nil, fmt.Errorf("request field %s: %w", field.Name, locationErr)
		}
		if location == "" {
			return nil, nil, fmt.Errorf("request field %s has no api.path/query/header/body annotation", field.Name)
		}
		if name == "" {
			return nil, nil, fmt.Errorf("request field %s has an empty api.%s name", field.Name, location)
		}
		property, err := typeSchema(field.Type, structs, docs)
		if err != nil {
			return nil, nil, fmt.Errorf("request field %s: %w", field.Name, err)
		}
		applyFieldSemantics(property, name)
		if location == "body" {
			if _, exists := bodyProperties[name]; exists {
				return nil, nil, fmt.Errorf("request field %s duplicates body name %q", field.Name, name)
			}
			bodyProperties[name] = property
			if field.Requiredness == parser.FieldType_Required {
				bodyRequired[name] = struct{}{}
			}
			continue
		}
		if location == "path" {
			if _, expected := pathNames[name]; !expected {
				return nil, nil, fmt.Errorf("request field %s path name %q is not present in route %s", field.Name, name, operation.Path)
			}
			if field.Requiredness != parser.FieldType_Required {
				return nil, nil, fmt.Errorf("request field %s path parameter %q must be required", field.Name, name)
			}
			if _, exists := seenPathNames[name]; exists {
				return nil, nil, fmt.Errorf("request field %s duplicates path name %q", field.Name, name)
			}
			seenPathNames[name] = struct{}{}
		}
		parameterKey := location + ":" + name
		if _, exists := seenParameters[parameterKey]; exists {
			return nil, nil, fmt.Errorf("request field %s duplicates %s parameter %q", field.Name, location, name)
		}
		seenParameters[parameterKey] = struct{}{}
		parameter := map[string]any{"name": name, "in": location, "required": field.Requiredness == parser.FieldType_Required, "schema": property}
		if location == "header" {
			parameter["style"] = "simple"
		}
		parameters = append(parameters, parameter)
	}
	for name := range pathNames {
		if _, exists := seenPathNames[name]; !exists {
			return nil, nil, fmt.Errorf("route %s path parameter %q has no request field", operation.Path, name)
		}
	}
	sort.Slice(parameters, func(i, j int) bool {
		a, _ := parameters[i].(map[string]any)
		b, _ := parameters[j].(map[string]any)
		return a["name"].(string) < b["name"].(string)
	})
	if len(bodyProperties) == 0 {
		return parameters, nil, nil
	}
	required := make([]string, 0, len(bodyRequired))
	for name := range bodyRequired {
		if _, ok := bodyProperties[name]; ok {
			required = append(required, name)
		} else {
			return nil, nil, fmt.Errorf("operation %s required_body contains unknown field %q", operation.Name, name)
		}
	}
	sort.Strings(required)
	bodySchema := map[string]any{"type": "object", "properties": bodyProperties, "additionalProperties": false}
	if len(required) > 0 {
		bodySchema["required"] = required
	}
	return parameters, map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": bodySchema}}}, nil
}

func fieldLocationStrict(field *parser.Field) (string, string, error) {
	if field == nil {
		return "", "", errors.New("field is nil")
	}
	var location, name string
	for _, annotation := range field.Annotations {
		if annotation == nil || len(annotation.Values) != 1 {
			continue
		}
		candidateLocation := ""
		switch annotation.Key {
		case "api.path":
			candidateLocation = "path"
		case "api.query":
			candidateLocation = "query"
		case "api.header":
			candidateLocation = "header"
		case "api.body":
			candidateLocation = "body"
		default:
			continue
		}
		if location != "" {
			return "", "", fmt.Errorf("multiple api location annotations")
		}
		location, name = candidateLocation, annotation.Values[0]
	}
	return location, name, nil
}

func pathParameterNames(path string) map[string]struct{} {
	result := map[string]struct{}{}
	for _, part := range strings.Split(path, "/") {
		if strings.HasPrefix(part, ":") && len(part) > 1 {
			result[strings.TrimPrefix(part, ":")] = struct{}{}
		}
	}
	return result
}

func defaultResponseHeaders(operation operation) []string {
	if operation.Doc.SuccessStatus == 204 {
		return []string{"X-Request-ID", "X-Trace-ID"}
	}
	return []string{"X-Request-ID", "X-Trace-ID"}
}

func defaultErrorResponseHeaders(status int) []string {
	headers := []string{"X-Request-ID", "X-Trace-ID"}
	switch status {
	case 401:
		headers = append(headers, "WWW-Authenticate")
	case 429:
		headers = append(headers, "Retry-After")
	}
	return headers
}

func responseHeadersSchema(names []string) map[string]any {
	result := map[string]any{}
	for _, name := range names {
		description := "响应关联的请求标识。"
		schema := map[string]any{"schema": map[string]any{"type": "string"}, "description": description}
		switch strings.ToLower(name) {
		case "etag":
			schema["description"] = "强 ETag；写操作后用于下一次 If-Match。"
		case "location":
			schema["schema"] = map[string]any{"type": "string", "format": "uri"}
		case "retry-after":
			schema["schema"] = map[string]any{"type": "integer", "format": "int32", "minimum": 1}
		case "www-authenticate":
			schema["description"] = "Bearer 认证挑战。"
		case "cache-control":
			schema["description"] = "缓存策略；下载重定向为 private, no-store。"
		}
		result[name] = schema
	}
	return result
}

func buildAsyncAPI(value webSocketMetadata) map[string]any {
	messages := map[string]any{}
	messageRefs := map[string]any{}
	for _, message := range value.Messages {
		messages[message.Name] = map[string]any{
			"name":        message.Name,
			"title":       message.Summary,
			"summary":     message.Summary,
			"description": message.Description,
			"contentType": "application/octet-stream",
			"payload":     map[string]any{"type": "string", "format": "binary", "description": "原始 WebSocket 二进制帧；由 y-sync/awareness 协议解释。"},
		}
		messageRefs[message.Name] = map[string]any{"$ref": "#/components/messages/" + message.Name}
	}
	channelMessages := map[string]any{}
	for name, ref := range messageRefs {
		channelMessages[name] = ref
	}
	closeCodes := make([]any, 0, len(value.CloseCodes))
	for _, code := range value.CloseCodes {
		closeCodes = append(closeCodes, map[string]any{"code": code.Code, "reason": code.Reason, "description": code.Description})
	}
	return map[string]any{
		"asyncapi": "3.0.0",
		"info":     map[string]any{"title": value.Title, "version": "1.0.0", "description": value.Description},
		"servers":  map[string]any{"development": map[string]any{"host": "localhost:8091", "protocol": "ws", "pathname": value.Address, "description": "本地 Collaboration WebSocket；生产环境使用 wss。"}},
		"channels": map[string]any{"document": map[string]any{"address": value.Address, "description": "单个文档的 y-sync 与 awareness 双向通道。连接前必须通过 Gateway 创建一次性协作 session。", "messages": channelMessages, "bindings": map[string]any{"ws": map[string]any{"method": "GET", "query": map[string]any{"type": "object", "additionalProperties": false}}}}},
		"operations": map[string]any{
			"receiveDocumentFrames": map[string]any{"action": "receive", "channel": map[string]any{"$ref": "#/channels/document"}, "summary": "客户端向 Collaboration 发送二进制帧。"},
			"sendDocumentFrames":    map[string]any{"action": "send", "channel": map[string]any{"$ref": "#/channels/document"}, "summary": "Collaboration 向客户端广播已提交的二进制帧。"},
		},
		"components": map[string]any{
			"messages":        messages,
			"schemas":         map[string]any{"DocumentId": map[string]any{"type": "string", "format": "uuid", "description": "规范 UUIDv7 文档 ID。"}},
			"securitySchemes": map[string]any{"sessionTicket": map[string]any{"type": "apiKey", "in": "header", "name": "Sec-WebSocket-Protocol", "description": "值为 knowledge-core-yjs-v1, ticket.<opaque>；ticket 由 Gateway session API 返回且只能使用一次。"}},
		},
		"x-handshake":   map[string]any{"path_parameter": "document_id", "origin": "必须匹配 Collaboration 的精确允许 Origin。", "subprotocol": value.Subprotocol, "ticket": "短期、单次使用；过期或重复使用返回 401。", "failure_statuses": []int{400, 401, 403, 429, 503}},
		"x-close-codes": closeCodes,
	}
}

type htmlOperation struct {
	Tag, Name, Method, Path, Summary, Description, Security, Response string
}

type htmlPage struct {
	Title       string
	Description string
	Operations  []htmlOperation
	Schemas     []string
	Async       bool
	Subprotocol string
	Address     string
	CloseCodes  []webSocketClose
}

func buildHTML(operations []operation, structs map[string]*parser.StructLike, asyncAPI map[string]any, wsMetadata webSocketMetadata) (map[string][]byte, error) {
	byTag := make(map[string][]htmlOperation)
	for _, operation := range operations {
		byTag[operation.Doc.Tag] = append(byTag[operation.Doc.Tag], htmlOperation{Tag: operation.Doc.Tag, Name: operation.Name, Method: operation.Method, Path: colonPathToOpenAPI(operation.Path), Summary: operation.Doc.Summary, Description: operation.Doc.Description, Security: operation.Doc.Security, Response: strconv.Itoa(operation.Doc.SuccessStatus)})
	}
	tags := make([]string, 0, len(byTag))
	for tag := range byTag {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	orderedOperations := make([]htmlOperation, 0, len(operations))
	for _, tag := range tags {
		orderedOperations = append(orderedOperations, byTag[tag]...)
	}
	schemaNames := make([]string, 0, len(structs))
	for name := range structs {
		schemaNames = append(schemaNames, name)
	}
	sort.Strings(schemaNames)
	openPage, err := renderTemplate(htmlPage{Title: "Knowledge Core Gateway HTTP API", Description: "只读 HTTP 契约；详细 schema 和机器可读文件见本页链接。", Operations: orderedOperations, Schemas: schemaNames})
	if err != nil {
		return nil, err
	}
	wsPage, err := renderTemplate(htmlPage{Title: "Knowledge Core Collaboration WebSocket", Description: asyncAPI["info"].(map[string]any)["description"].(string), Async: true, Subprotocol: wsMetadata.Subprotocol, Address: wsMetadata.Address, CloseCodes: wsMetadata.CloseCodes})
	if err != nil {
		return nil, err
	}
	index, err := renderIndex()
	if err != nil {
		return nil, err
	}
	return map[string][]byte{indexHTMLFile: index, httpHTMLFile: openPage, wsHTMLFile: wsPage, styleFile: []byte(styleCSS)}, nil
}

func renderTemplate(page htmlPage) ([]byte, error) {
	const source = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>{{.Title}}</title><link rel="stylesheet" href="/docs/assets/style.css"></head>
<body><main><p><a href="/docs/">← 文档首页</a></p><h1>{{.Title}}</h1><p class="lead">{{.Description}}</p>{{if .Async}}<section><h2>连接流程</h2><ol><li>调用 Gateway 的协作 session API。</li><li>把返回的 document ID 和一次性 ticket 放入 WebSocket URL 与 <code>Sec-WebSocket-Protocol</code>。</li><li>使用 <code>{{.Subprotocol}}</code> 和二进制 y-sync/awareness 帧。</li></ol><h2>握手约束</h2><p>路径为 <code>{{.Address}}</code>；document ID 必须是 UUIDv7，Origin 必须精确匹配允许列表。ticket 只能使用一次并在短期内过期。</p><h2>关闭码</h2><table><thead><tr><th>代码</th><th>原因</th><th>说明</th></tr></thead><tbody>{{range .CloseCodes}}<tr><td>{{.Code}}</td><td>{{.Reason}}</td><td>{{.Description}}</td></tr>{{end}}</tbody></table></section>{{else}}<p><a class="button" href="/docs/openapi.yaml">下载 OpenAPI YAML</a> <a class="button" href="/docs/openapi.json">下载 OpenAPI JSON</a></p>{{range .Operations}}<article><div class="operation"><span class="method method-{{.Method}}">{{.Method}}</span><code>{{.Path}}</code></div><h2>{{.Summary}}</h2><p>{{.Description}}</p><p class="meta">operationId={{.Name}} · 成功响应 {{.Response}} · 鉴权 {{.Security}}</p></article>{{end}}<h2>Schema</h2><p>{{len .Schemas}} 个 Thrift 数据结构由 IDL 自动生成。</p>{{end}}</main></body></html>`
	tmpl, err := template.New("page").Parse(source)
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	if err := tmpl.Execute(&output, page); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func renderIndex() ([]byte, error) {
	const source = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Knowledge Core API 文档</title><link rel="stylesheet" href="/docs/assets/style.css"></head><body><main><h1>Knowledge Core API 文档</h1><p class="lead">Gateway HTTP 和 Collaboration WebSocket 的只读契约文档。</p><p><a class="button" href="/docs/http">HTTP API</a> <a class="button" href="/docs/websocket">WebSocket API</a></p><h2>机器可读规范</h2><ul><li><a href="/docs/openapi.yaml">OpenAPI YAML</a> · <a href="/docs/openapi.json">OpenAPI JSON</a></li><li><a href="/docs/asyncapi.yaml">AsyncAPI YAML</a> · <a href="/docs/asyncapi.json">AsyncAPI JSON</a></li></ul><p class="note">此页面由生成器产出，不支持在线调用；Admin 端口必须通过受控网络或端口转发访问。</p></main></body></html>`
	return []byte(source), nil
}

const styleCSS = `:root{color-scheme:light dark;font-family:system-ui,-apple-system,"Segoe UI",sans-serif;line-height:1.6}body{margin:0;background:#f7f7f8;color:#202124}main{max-width:1000px;margin:0 auto;padding:2rem 1.25rem 4rem}a{color:#1261a0}h1{margin-top:0;font-size:2rem}.lead{font-size:1.1rem;color:#5f6368}.button{display:inline-block;margin:.25rem .5rem .25rem 0;padding:.5rem .8rem;border:1px solid #8ab4f8;border-radius:.4rem;text-decoration:none}.operation{display:flex;gap:.7rem;align-items:center;flex-wrap:wrap}.method{font-size:.75rem;font-weight:700;padding:.15rem .4rem;border-radius:.3rem;color:#fff}.method-GET{background:#188038}.method-POST{background:#1967d2}.method-PUT,.method-PATCH{background:#b06000}.method-DELETE{background:#c5221f}article{margin:1.2rem 0;padding:1rem 1.2rem;background:#fff;border:1px solid #dadce0;border-radius:.6rem}article h2{margin:.5rem 0;font-size:1.2rem}.meta,.note{font-size:.9rem;color:#5f6368}code{font-family:ui-monospace,SFMono-Regular,Menlo,monospace}table{border-collapse:collapse;background:#fff}th,td{border:1px solid #dadce0;padding:.45rem .7rem;text-align:left}@media(prefers-color-scheme:dark){body{background:#202124;color:#e8eaed}.lead,.meta,.note{color:#bdc1c6}article,table{background:#292a2d;border-color:#5f6368}th,td{border-color:#5f6368}.button{border-color:#8ab4f8}}`

func writeOrCheck(root string, value document, check bool) error {
	outputDir := filepath.Join(root, "api")
	openAPIYAML, err := marshalYAML(value.OpenAPI)
	if err != nil {
		return fmt.Errorf("marshal OpenAPI YAML: %w", err)
	}
	asyncAPIYAML, err := marshalYAML(value.AsyncAPI)
	if err != nil {
		return fmt.Errorf("marshal AsyncAPI YAML: %w", err)
	}
	openAPIJSONBytes, err := marshalJSON(value.OpenAPI)
	if err != nil {
		return fmt.Errorf("marshal OpenAPI JSON: %w", err)
	}
	asyncAPIJSONBytes, err := marshalJSON(value.AsyncAPI)
	if err != nil {
		return fmt.Errorf("marshal AsyncAPI JSON: %w", err)
	}
	files := map[string][]byte{openAPIFile: openAPIYAML, asyncAPIFile: asyncAPIYAML, openAPIJSON: openAPIJSONBytes, asyncAPIJSON: asyncAPIJSONBytes}
	for name, contents := range value.HTML {
		files[name] = contents
	}
	if check {
		var mismatch []string
		for name, expected := range files {
			actual, err := os.ReadFile(filepath.Join(outputDir, name))
			if err != nil {
				mismatch = append(mismatch, name+": "+err.Error())
				continue
			}
			if !bytes.Equal(actual, expected) {
				mismatch = append(mismatch, name)
			}
		}
		if len(mismatch) > 0 {
			sort.Strings(mismatch)
			return fmt.Errorf("generated API documentation is stale: %s", strings.Join(mismatch, ", "))
		}
		return nil
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("create API documentation directory: %w", err)
	}
	for name, contents := range files {
		path := filepath.Join(outputDir, name)
		if err := os.WriteFile(path, contents, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	return nil
}

func marshalJSON(value any) ([]byte, error) {
	contents, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(contents, '\n'), nil
}

func marshalYAML(value any) ([]byte, error) {
	jsonContents, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var node any
	if err := json.Unmarshal(jsonContents, &node); err != nil {
		return nil, err
	}
	contents, err := yaml.Marshal(node)
	if err != nil {
		return nil, err
	}
	return contents, nil
}

func httpStatusDescription(status int) string {
	descriptions := map[int]string{200: "请求成功。", 201: "资源已创建。", 202: "请求已接受。", 204: "请求成功且无响应正文。", 303: "资源通过 Location 临时重定向。", 400: "请求格式或参数无效。", 401: "需要认证或认证已失效。", 403: "没有执行该操作的权限。", 404: "资源不存在。", 409: "资源状态冲突。", 410: "资源已永久不可用。", 412: "If-Match 或其他前置条件不满足。", 423: "账户或资源暂时锁定。", 429: "请求频率超过限制。", 500: "Gateway 内部错误。", 502: "上游响应无效或不可用。", 503: "依赖或 Gateway 暂不可用。", 504: "上游请求超时。"}
	if value, ok := descriptions[status]; ok {
		return value
	}
	return fmt.Sprintf("HTTP %d 响应。", status)
}
