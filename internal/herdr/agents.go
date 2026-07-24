package herdr

import "context"

// Agent operations. target is always a live agent name or the hosting pane id
// (never a terminal id or a bare kind). The relay resolves targets from the
// snapshot and never relies on Herdr UI focus.

type agentPromptWait struct {
	Until     []AgentStatus `json:"until,omitempty"`
	TimeoutMS *int          `json:"timeout_ms,omitempty"`
}

type agentPromptParams struct {
	Target string           `json:"target"`
	Text   string           `json:"text"`
	Wait   *agentPromptWait `json:"wait,omitempty"`
}

type agentPromptedResult struct {
	Type  string `json:"type"`
	Agent Agent  `json:"agent"`
}

// PromptOptions configure a prompt submission. When Wait is true the call
// blocks until the agent settles (or the given until-states are reached).
type PromptOptions struct {
	Wait      bool
	Until     []AgentStatus
	TimeoutMS int
}

// AgentPrompt atomically submits text plus Enter to an agent. With Wait, Herdr
// blocks for a settled lifecycle state.
func (c *Client) AgentPrompt(ctx context.Context, target, text string, opts PromptOptions) (*Agent, error) {
	if target == "" {
		return nil, newError("invalid_params", "agent.prompt requires a target")
	}
	params := agentPromptParams{Target: target, Text: text}
	if opts.Wait || len(opts.Until) > 0 || opts.TimeoutMS > 0 {
		w := &agentPromptWait{Until: opts.Until}
		if opts.TimeoutMS > 0 {
			w.TimeoutMS = &opts.TimeoutMS
		}
		params.Wait = w
	}
	var res agentPromptedResult
	if err := c.call(ctx, "agent.prompt", params, "agent_prompted", &res); err != nil {
		return nil, err
	}
	return &res.Agent, nil
}

type agentSendKeysParams struct {
	Target string   `json:"target"`
	Keys   []string `json:"keys"`
}

// AgentSendKeys sends validated logical keys to an agent surface. Every key is
// validated before any byte reaches Herdr.
func (c *Client) AgentSendKeys(ctx context.Context, target string, keys []string) error {
	if target == "" {
		return newError("invalid_params", "agent.send_keys requires a target")
	}
	if bad, ok := ValidateKeys(keys); !ok {
		if bad == "" {
			return newError("invalid_params", "agent.send_keys requires at least one key")
		}
		return newError("invalid_params", "agent.send_keys rejected invalid key: "+bad)
	}
	return c.call(ctx, "agent.send_keys", agentSendKeysParams{Target: target, Keys: keys}, "ok", nil)
}

type agentRenameParams struct {
	Target string  `json:"target"`
	Name   *string `json:"name"`
}

type agentInfoResultAlias = agentInfoResult // reuse the shape from reads.go

// AgentRename sets or clears an agent's name. An empty name clears it. The name
// must match Herdr's identifier rule.
func (c *Client) AgentRename(ctx context.Context, target, name string) (*Agent, error) {
	if target == "" {
		return nil, newError("invalid_params", "agent.rename requires a target")
	}
	if name != "" && !ValidAgentName(name) {
		return nil, newError("invalid_params", "agent name must match [a-z][a-z0-9_-]{0,31}")
	}
	var res agentInfoResultAlias
	if err := c.call(ctx, "agent.rename", agentRenameParams{Target: target, Name: optStr(name)}, "agent_info", &res); err != nil {
		return nil, err
	}
	return &res.Agent, nil
}

// AgentFocus focuses the pane hosting an agent, marking it seen.
func (c *Client) AgentFocus(ctx context.Context, target string) (*Agent, error) {
	if target == "" {
		return nil, newError("invalid_params", "agent.focus requires a target")
	}
	var res agentInfoResultAlias
	if err := c.call(ctx, "agent.focus", agentTarget{Target: target}, "agent_info", &res); err != nil {
		return nil, err
	}
	return &res.Agent, nil
}

type agentStartParams struct {
	Name      string   `json:"name"`
	Kind      string   `json:"kind"`
	PaneID    string   `json:"pane_id"`
	Args      []string `json:"args,omitempty"`
	TimeoutMS *int     `json:"timeout_ms,omitempty"`
}

// AgentStarted is the result of starting an agent.
type AgentStarted struct {
	Type  string   `json:"type"`
	Agent Agent    `json:"agent"`
	Argv  []string `json:"argv"`
}

// StartOptions configure starting an agent in an available shell pane.
type StartOptions struct {
	Name      string
	Kind      string
	PaneID    string
	Args      []string
	TimeoutMS int
}

// AgentStart starts a discovered agent kind in an existing available pane. It
// never creates or moves layout. Kind must be one the server advertises; the
// caller resolves the kind list from Herdr rather than hard-coding it.
func (c *Client) AgentStart(ctx context.Context, opts StartOptions) (*AgentStarted, error) {
	if opts.PaneID == "" {
		return nil, newError("invalid_params", "agent.start requires a pane id")
	}
	if opts.Kind == "" {
		return nil, newError("invalid_params", "agent.start requires a kind")
	}
	if !ValidAgentName(opts.Name) {
		return nil, newError("invalid_params", "agent name must match [a-z][a-z0-9_-]{0,31}")
	}
	params := agentStartParams{Name: opts.Name, Kind: opts.Kind, PaneID: opts.PaneID, Args: opts.Args}
	if opts.TimeoutMS > 0 {
		params.TimeoutMS = &opts.TimeoutMS
	}
	var res AgentStarted
	if err := c.call(ctx, "agent.start", params, "agent_started", &res); err != nil {
		return nil, err
	}
	return &res, nil
}

type agentWaitParams struct {
	Target    string        `json:"target"`
	Until     []AgentStatus `json:"until,omitempty"`
	TimeoutMS *int          `json:"timeout_ms,omitempty"`
}

// AgentWait waits for an agent to reach a settled (or specified) state.
func (c *Client) AgentWait(ctx context.Context, target string, until []AgentStatus, timeoutMS int) (*Agent, error) {
	if target == "" {
		return nil, newError("invalid_params", "agent.wait requires a target")
	}
	params := agentWaitParams{Target: target, Until: until}
	if timeoutMS > 0 {
		params.TimeoutMS = &timeoutMS
	}
	var res agentInfoResultAlias
	if err := c.call(ctx, "agent.wait", params, "agent_info", &res); err != nil {
		return nil, err
	}
	return &res.Agent, nil
}

// ValidAgentName reports whether name matches Herdr's rule [a-z][a-z0-9_-]{0,31}.
func ValidAgentName(name string) bool {
	if name == "" || len(name) > 32 {
		return false
	}
	if name[0] < 'a' || name[0] > 'z' {
		return false
	}
	for i := 1; i < len(name); i++ {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '-' {
			continue
		}
		return false
	}
	return true
}
