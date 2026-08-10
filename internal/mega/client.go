package mega

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	goMega "github.com/t3rm1n4l/go-mega"
)

const (
	defaultAPIURL        = "https://g.api.mega.co.nz/cs"
	maxAPIResponse       = 32 << 20
	defaultClientTimeout = 30 * time.Second
)

// ResolvedJob is the metadata needed to create a downloader job without
// fetching any payload bytes.
type ResolvedJob struct {
	Kind        LinkKind
	DisplayName string
	TotalBytes  int64
	Files       []ResolvedFile
}

// ResolvedFile contains public metadata and the payload URL acquired from
// MEGA. FileKey is the raw 32-byte file key; Key contains the derived crypto
// fields needed by the transfer engine. Both are memory-only secrets.
type ResolvedFile struct {
	NodeID         string
	RelativePath   string
	Size           int64
	FileKey        []byte
	Attributes     map[string]string
	Key            FileKey
	PayloadURL     string
	PayloadContext string
}

// Client owns the project-specific public-link protocol path. It deliberately
// does not delegate public links or payload URL acquisition to go-mega.
type Client struct {
	httpClient *http.Client
	apiURL     string
	sequence   atomic.Uint64
	session    string
}

// WithSession returns a lightweight client sharing the HTTP transport and API
// endpoint while authenticating API commands with the selected account. The
// session is never serialized or included in an error string.
func (c *Client) WithSession(session string) *Client {
	if c == nil {
		return nil
	}
	return &Client{httpClient: c.httpClient, apiURL: c.apiURL, session: session}
}

func (c *Client) HTTPClient() *http.Client {
	if c == nil {
		return nil
	}
	return c.httpClient
}
func (c *Client) WithHTTPClient(client *http.Client) *Client {
	if c == nil {
		return nil
	}
	return &Client{httpClient: client, apiURL: c.apiURL, session: c.session}
}

// LoginAccount uses the maintained go-mega authentication primitive and
// returns only its opaque session identifier. MFA-required errors are passed
// through so callers can present the explicit unsupported-account message.
func (c *Client) LoginAccount(email, password string) (string, error) {
	if c == nil || c.httpClient == nil {
		return "", fmt.Errorf("MEGA HTTP client is unavailable")
	}
	account := goMega.New().SetClient(c.httpClient).SetLogger(nil).SetDebugger(nil)
	account.SetAPIUrl(strings.TrimSuffix(c.apiURL, "/cs"))
	if err := account.Login(email, password); err != nil {
		return "", err
	}
	return account.GetSessionID(), nil
}

// NewClient constructs a public-link client. apiBaseURL may be either the
// MEGA API root or a complete /cs endpoint; an empty value selects production.
func NewClient(httpClient *http.Client, apiBaseURL string) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultClientTimeout}
	}
	apiURL := strings.TrimRight(apiBaseURL, "/")
	if apiURL == "" {
		apiURL = defaultAPIURL
	} else if !strings.HasSuffix(apiURL, "/cs") {
		apiURL += "/cs"
	}
	client := &Client{httpClient: httpClient, apiURL: apiURL}
	return client
}

// CloseIdleConnections releases pooled transport connections owned by the
// application during shutdown.
func (c *Client) CloseIdleConnections() {
	if c != nil && c.httpClient != nil {
		c.httpClient.CloseIdleConnections()
	}
}

// FetchPayloadRange performs one bounded request against an already resolved
// MEGA payload URL. Payload URLs are opaque and may contain secrets, so the
// URL is never included in an error message or log field here.
func (c *Client) FetchPayloadRange(ctx context.Context, payloadURL string, start, end int64) (*http.Response, error) {
	if c == nil || c.httpClient == nil {
		return nil, fmt.Errorf("MEGA HTTP client is unavailable")
	}
	if payloadURL == "" || start < 0 || end < start {
		return nil, fmt.Errorf("invalid MEGA payload range")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, payloadURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create MEGA payload request: %w", err)
	}
	request.Header.Set("Range", "bytes="+strconv.FormatInt(start, 10)+"-"+strconv.FormatInt(end, 10))
	request.Header.Set("Accept", "application/octet-stream")
	response, err := c.httpClient.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// net/http includes the full request URL in some transport errors. A
		// payload URL is encrypted at rest and must not escape through a
		// persisted diagnostic string.
		return nil, fmt.Errorf("MEGA payload request failed")
	}
	return response, nil
}

// RefreshPayloadURL requests a new opaque payload URL for an already-created
// file. It deliberately performs metadata-only protocol work and never
// downloads file bytes.
func (c *Client) RefreshPayloadURL(ctx context.Context, link PublicLink, nodeID string) (string, error) {
	if link.Kind == LinkKindFile {
		return c.payloadFromCommand(ctx, map[string]any{"a": "g", "g": 1, "p": link.Handle}, "")
	}
	return c.payloadFromCommand(ctx, map[string]any{"a": "g", "g": 1, "n": nodeID}, link.Handle)
}

func (c *Client) payloadFromCommand(ctx context.Context, command map[string]any, folderHandle string) (string, error) {
	response, err := c.command(ctx, []map[string]any{command}, folderHandle)
	if err != nil {
		return "", err
	}
	object, err := firstObject(response)
	if err != nil {
		return "", err
	}
	payload := stringValue(object["g"])
	if payload == "" {
		return "", fmt.Errorf("%w: refreshed payload URL is missing", ErrInvalidLink)
	}
	return payload, nil
}

// ResolveLink resolves a public file or folder link. accountID is accepted for
// the stable future-facing contract but is intentionally unused in this
// anonymous protocol spike; authenticated account plumbing belongs to Phase F.
func (c *Client) ResolveLink(ctx context.Context, rawURL, accountID string) (ResolvedJob, error) {
	link, err := ParseLink(rawURL)
	if err != nil {
		return ResolvedJob{}, err
	}
	if link.Kind == LinkKindFile {
		file, err := c.resolveFile(ctx, link)
		if err != nil {
			return ResolvedJob{}, err
		}
		return ResolvedJob{
			Kind:        LinkKindFile,
			DisplayName: file.RelativePath,
			TotalBytes:  file.Size,
			Files:       []ResolvedFile{file},
		}, nil
	}
	return c.resolveFolder(ctx, link)
}

func (c *Client) resolveFile(ctx context.Context, link PublicLink) (ResolvedFile, error) {
	key, err := DecodeFileKey(link.Key)
	if err != nil {
		return ResolvedFile{}, err
	}
	response, err := c.command(ctx, []map[string]any{{"a": "g", "g": 1, "p": link.Handle}}, "")
	if err != nil {
		return ResolvedFile{}, err
	}
	object, err := firstObject(response)
	if err != nil {
		return ResolvedFile{}, err
	}
	file, err := c.fileFromMetadata(object, link.Handle, "", key)
	if err != nil {
		return ResolvedFile{}, err
	}
	return file, nil
}

func (c *Client) resolveFolder(ctx context.Context, link PublicLink) (ResolvedJob, error) {
	masterKey, err := DecodeNodeKey(link.Key)
	if err != nil {
		return ResolvedJob{}, err
	}
	response, err := c.command(ctx, []map[string]any{{"a": "f", "c": 1, "r": 1, "ca": 1}}, link.Handle)
	if err != nil {
		return ResolvedJob{}, err
	}
	root, err := firstObject(response)
	if err != nil {
		return ResolvedJob{}, err
	}
	items, ok := root["f"].([]any)
	if !ok {
		return ResolvedJob{}, fmt.Errorf("%w: folder response has no node list", ErrInvalidLink)
	}
	rootHandle, err := publicFolderRootHandle(items, link.Handle)
	if err != nil {
		return ResolvedJob{}, err
	}

	nodes := make([]folderNode, 0, len(items))
	for index, raw := range items {
		object, ok := raw.(map[string]any)
		if !ok {
			return ResolvedJob{}, fmt.Errorf("%w: folder node %d is not an object", ErrInvalidLink, index)
		}
		node, err := c.decodeFolderNode(object, rootHandle, masterKey)
		if err != nil {
			return ResolvedJob{}, err
		}
		nodes = append(nodes, node)
	}
	byHandle := make(map[string]*folderNode, len(nodes))
	for index := range nodes {
		if nodes[index].Handle != "" {
			byHandle[nodes[index].Handle] = &nodes[index]
		}
	}

	rootName := rootHandle
	if rootNode := byHandle[rootHandle]; rootNode != nil && rootNode.Name != "" {
		rootName = rootNode.Name
	}
	files := make([]ResolvedFile, 0)
	var total int64
	for index := range nodes {
		node := &nodes[index]
		if node.Kind != 0 {
			continue
		}
		if node.Size < 0 {
			return ResolvedJob{}, fmt.Errorf("%w: file %q has a negative size", ErrInvalidLink, node.Handle)
		}
		if total > math.MaxInt64-node.Size {
			return ResolvedJob{}, fmt.Errorf("%w: folder size overflows int64", ErrInvalidLink)
		}
		relativePath, err := folderNodePath(node, byHandle)
		if err != nil {
			return ResolvedJob{}, err
		}
		payloadURL, err := c.payloadURL(ctx, node.Handle, link.Handle)
		if err != nil {
			return ResolvedJob{}, err
		}
		file := ResolvedFile{
			NodeID:         node.Handle,
			RelativePath:   relativePath,
			Size:           node.Size,
			FileKey:        append([]byte(nil), node.FileKey.Raw[:]...),
			Attributes:     cloneStringMap(node.Attributes),
			Key:            node.FileKey,
			PayloadURL:     payloadURL,
			PayloadContext: link.Handle,
		}
		files = append(files, file)
		total += node.Size
	}
	if len(files) == 0 {
		return ResolvedJob{}, fmt.Errorf("%w: folder contains no files", ErrInvalidLink)
	}
	return ResolvedJob{Kind: LinkKindFolder, DisplayName: rootName, TotalBytes: total, Files: files}, nil
}

func (c *Client) decodeFolderNode(object map[string]any, rootHandle string, masterKey NodeKey) (folderNode, error) {
	node := folderNode{Handle: stringValue(object["h"]), Parent: stringValue(object["p"])}
	if node.Handle == "" {
		return folderNode{}, fmt.Errorf("%w: folder node has no handle", ErrInvalidLink)
	}
	kind, ok := numberValue(object["t"])
	if !ok {
		return folderNode{}, fmt.Errorf("%w: node %q has an invalid type", ErrInvalidLink, node.Handle)
	}
	node.Kind = int(kind)
	if rawSize, present := object["s"]; present {
		node.Size, ok = numberValue(rawSize)
		if !ok {
			return folderNode{}, fmt.Errorf("%w: node %q has an invalid size", ErrInvalidLink, node.Handle)
		}
	}
	if node.Size < 0 {
		return folderNode{}, fmt.Errorf("%w: node %q has a negative size", ErrInvalidLink, node.Handle)
	}

	var aesKey []byte
	if node.Handle == rootHandle {
		// A public listing retains the root node's parent from the owner's
		// private tree even though that parent is outside the share. Normalize
		// the public root boundary so path construction cannot escape it.
		node.Parent = ""
	}
	if node.Handle == rootHandle && node.Kind != 0 && object["k"] == nil {
		node.NodeKey = masterKey
		aesKey = masterKey.AESKey[:]
	} else {
		encryptedKey := extractNodeKey(object["k"], rootHandle)
		if encryptedKey == "" {
			return folderNode{}, fmt.Errorf("%w: node %q has no encrypted key", ErrInvalidLink, node.Handle)
		}
		decrypted, err := DecryptNodeKey(encryptedKey, masterKey)
		if err != nil {
			return folderNode{}, fmt.Errorf("decode node %q: %w", node.Handle, err)
		}
		if node.Kind == 0 {
			node.FileKey, err = decodeFileKeyBytes(decrypted)
			if err != nil {
				return folderNode{}, fmt.Errorf("decode file node %q: %w", node.Handle, err)
			}
			aesKey = node.FileKey.AESKey[:]
		} else {
			node.NodeKey, err = decodeNodeKeyBytes(decrypted)
			if err != nil {
				return folderNode{}, fmt.Errorf("decode folder node %q: %w", node.Handle, err)
			}
			aesKey = node.NodeKey.AESKey[:]
		}
	}

	encodedAttributes := stringValue(object["a"])
	if encodedAttributes != "" {
		attributes, err := DecryptAttributes(encodedAttributes, aesKey)
		if err != nil {
			return folderNode{}, fmt.Errorf("decode attributes for node %q: %w", node.Handle, err)
		}
		node.Attributes = attributeStrings(attributes)
		node.Name = node.Attributes["n"]
	}
	if node.Name == "" {
		node.Name = node.Handle
	}
	return node, nil
}

func publicFolderRootHandle(items []any, publicHandle string) (string, error) {
	for index, raw := range items {
		object, ok := raw.(map[string]any)
		if !ok {
			return "", fmt.Errorf("%w: folder node %d is not an object", ErrInvalidLink, index)
		}
		handle := stringValue(object["h"])
		if handle != "" && hasNodeKeyOwner(object["k"], handle) {
			return handle, nil
		}
	}

	// Some deterministic/legacy responses omit the root key because the
	// public link key is already the root node key.
	for _, raw := range items {
		object := raw.(map[string]any)
		if stringValue(object["h"]) == publicHandle && object["k"] == nil {
			return publicHandle, nil
		}
	}
	return "", fmt.Errorf("%w: public folder root is missing", ErrInvalidLink)
}

func hasNodeKeyOwner(value any, owner string) bool {
	encoded, ok := value.(string)
	if !ok {
		return false
	}
	for _, candidate := range strings.Split(encoded, "/") {
		candidateOwner, _, found := strings.Cut(candidate, ":")
		if found && candidateOwner == owner {
			return true
		}
	}
	return false
}

func (c *Client) fileFromMetadata(object map[string]any, handle, folderHandle string, key FileKey) (ResolvedFile, error) {
	size, ok := numberValue(object["s"])
	if !ok {
		return ResolvedFile{}, fmt.Errorf("%w: file metadata has an invalid size", ErrInvalidLink)
	}
	if size < 0 {
		return ResolvedFile{}, fmt.Errorf("%w: negative file size", ErrInvalidLink)
	}
	encodedAttributes := stringValue(object["at"])
	if encodedAttributes == "" {
		return ResolvedFile{}, fmt.Errorf("%w: file metadata has no attributes", ErrInvalidLink)
	}
	attributes, err := DecryptAttributes(encodedAttributes, key.AESKey[:])
	if err != nil {
		return ResolvedFile{}, err
	}
	payloadURL := stringValue(object["g"])
	if payloadURL == "" {
		return ResolvedFile{}, fmt.Errorf("%w: file metadata has no payload URL", ErrInvalidLink)
	}
	name := stringValue(attributes["n"])
	if name == "" {
		name = handle
	}
	return ResolvedFile{
		NodeID:         handle,
		RelativePath:   name,
		Size:           size,
		FileKey:        append([]byte(nil), key.Raw[:]...),
		Attributes:     attributeStrings(attributes),
		Key:            key,
		PayloadURL:     payloadURL,
		PayloadContext: folderHandle,
	}, nil
}

func (c *Client) payloadURL(ctx context.Context, nodeHandle, folderHandle string) (string, error) {
	command := map[string]any{"a": "g", "g": 1, "n": nodeHandle}
	response, err := c.command(ctx, []map[string]any{command}, folderHandle)
	if err != nil {
		return "", err
	}
	object, err := firstObject(response)
	if err != nil {
		return "", err
	}
	payloadURL := stringValue(object["g"])
	if payloadURL == "" {
		return "", fmt.Errorf("%w: payload URL missing for node %q", ErrInvalidLink, nodeHandle)
	}
	return payloadURL, nil
}

func (c *Client) command(ctx context.Context, payload []map[string]any, folderHandle string) ([]any, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal MEGA command: %w", err)
	}
	endpoint, err := url.Parse(c.apiURL)
	if err != nil {
		return nil, fmt.Errorf("parse MEGA API URL: %w", err)
	}
	query := endpoint.Query()
	query.Set("id", strconv.FormatUint(c.sequence.Add(1), 10))
	if folderHandle != "" {
		query.Set("n", folderHandle)
	}
	if c.session != "" {
		query.Set("sid", c.session)
	}
	endpoint.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create MEGA API request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("MEGA API request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("MEGA API HTTP status %s", response.Status)
	}
	limited := io.LimitReader(response.Body, maxAPIResponse)
	decoder := json.NewDecoder(limited)
	decoder.UseNumber()
	var result []any
	if err := decoder.Decode(&result); err != nil {
		return nil, fmt.Errorf("decode MEGA API response: %w", err)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("%w: empty MEGA API response", ErrInvalidLink)
	}
	if code, ok := numberValue(result[0]); ok && code < 0 {
		return nil, &APIError{Code: int(code)}
	}
	return result, nil
}

type folderNode struct {
	Handle     string
	Parent     string
	Kind       int
	Size       int64
	Name       string
	Attributes map[string]string
	NodeKey    NodeKey
	FileKey    FileKey
}

func folderNodePath(node *folderNode, byHandle map[string]*folderNode) (string, error) {
	parts := []string{node.Name}
	seen := map[string]bool{node.Handle: true}
	parent := node.Parent
	for parent != "" {
		if seen[parent] {
			return "", fmt.Errorf("%w: folder node cycle at %q", ErrInvalidLink, parent)
		}
		seen[parent] = true
		parentNode := byHandle[parent]
		if parentNode == nil {
			return "", fmt.Errorf("%w: missing parent %q for node %q", ErrInvalidLink, parent, node.Handle)
		}
		parts = append(parts, parentNode.Name)
		parent = parentNode.Parent
	}
	for left, right := 0, len(parts)-1; left < right; left, right = left+1, right-1 {
		parts[left], parts[right] = parts[right], parts[left]
	}
	return strings.Join(parts, "/"), nil
}

func extractNodeKey(value any, ownerHandle string) string {
	encoded, ok := value.(string)
	if !ok || encoded == "" {
		return ""
	}
	for _, candidate := range strings.Split(encoded, "/") {
		owner, key, found := strings.Cut(candidate, ":")
		if found && owner == ownerHandle {
			return key
		}
	}
	return ""
}

func firstObject(result []any) (map[string]any, error) {
	if len(result) == 0 {
		return nil, fmt.Errorf("%w: empty response", ErrInvalidLink)
	}
	object, ok := result[0].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: response item is not an object", ErrInvalidLink)
	}
	return object, nil
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func numberValue(value any) (int64, bool) {
	switch number := value.(type) {
	case json.Number:
		parsed, err := strconv.ParseInt(string(number), 10, 64)
		return parsed, err == nil
	case float64:
		if number < math.MinInt64 || number > math.MaxInt64 || math.Trunc(number) != number {
			return 0, false
		}
		return int64(number), true
	case int:
		return int64(number), true
	case int64:
		return number, true
	default:
		return 0, false
	}
}

func attributeStrings(attributes map[string]any) map[string]string {
	result := make(map[string]string, len(attributes))
	for key, value := range attributes {
		if text, ok := value.(string); ok {
			result[key] = text
		}
	}
	return result
}

func cloneStringMap(values map[string]string) map[string]string {
	clone := make(map[string]string, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}
