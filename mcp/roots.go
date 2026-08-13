package mcp

// Root is a filesystem root the client exposes to the server (MCP roots
// capability). URI must be a file:// URI per the MCP spec.
type Root struct {
	URI  string `json:"uri"`
	Name string `json:"name,omitempty"`
}

// SetRoots installs the fixed set of roots reported to "roots/list" requests
// from the server and causes Initialize to declare the "roots" capability
// (listChanged: false — v1 has no dynamic root updates). Call before
// Initialize: the "roots" capability is only declared to the server when
// roots have been set.
func (c *Client) SetRoots(roots []Root) {
	rr := make([]Root, len(roots))
	copy(rr, roots)
	c.mu.Lock()
	c.roots = rr
	c.rootsSet = true
	c.mu.Unlock()
}

// rootsListResultWire is the wire shape of the client's reply to
// "roots/list".
type rootsListResultWire struct {
	Roots []Root `json:"roots"`
}

// handleRootsList responds to a server-initiated "roots/list" request with
// the roots installed via SetRoots. It is only reachable when roots have
// been set — the "roots" capability is only declared to the server in that
// case, and dispatchServerRequest gates the "roots/list" case on rootsSet,
// falling through to -32601 "Method not found" otherwise, mirroring the
// unknown-method path.
func (c *Client) handleRootsList(req serverRequest) {
	c.mu.Lock()
	roots := c.roots
	c.mu.Unlock()

	c.respondServerResult(req.ID, rootsListResultWire{Roots: roots})
}
