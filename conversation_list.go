package main

import (
	"context"
	"encoding/json"
	"fmt"
)

// Conversation resolution.
//
// /chat/completion refuses an empty or placeholder conversation id with a
// generic 710020202 "common invalid param". The id must name a real
// conversation, so a stateless caller has to obtain one before its first turn.
//
// The server offers a shortcut for exactly this case — option.need_create_
// conversation — but it is not sufficient on its own: with an empty id the
// request is still rejected. What works is reading the account's own
// conversation list and reusing the most recent thread.
//
// Reusing a thread is sound here because the plugin replaces the conversation's
// contents on every call: each request sends the whole prompt as the user turn
// and never reads prior messages back. The only observable effect is that the
// account's most recent thread accumulates turns in the upstream UI.

// recentConversation is one entry of the conversation list.
type recentConversation struct {
	ID           string `json:"id"`
	CellType     int    `json:"cell_type"`
	Conversation struct {
		ConversationID   string `json:"conversation_id"`
		ConversationType int    `json:"conversation_type"`
		ConvVersion      string `json:"conv_version"`
	} `json:"conversation"`
}

// conversationListResponse is the reply to the recent-conversation call.
type conversationListResponse struct {
	Cmd          int    `json:"cmd"`
	StatusCode   int    `json:"status_code"`
	StatusDesc   string `json:"status_desc"`
	DownlinkBody struct {
		PullRecent struct {
			Cells []recentConversation `json:"cells"`
		} `json:"pull_recent_conv_chain_downlink_body"`
	} `json:"downlink_body"`
}

// latestConversationID returns the account's most recent conversation id.
//
// It is called once per credential and cached by the caller, because the list
// changes slowly and the call is not free.
func (c *client) latestConversationID(ctx context.Context) (string, error) {
	body := uplinkMessage{
		Cmd:        cmdPullRecentConv,
		Channel:    2,
		SequenceID: randomUUID(),
		Version:    "1",
		UplinkBody: uplinkBody{PullRecentConvBody: &pullRecentConvBody{
			Limit:               20,
			MessageCountPerConv: 0,
			APIVersion:          1,
			ConvVersion:         0,
			Direction:           3,
			Option: pullRecentConvOption{
				NotNeedMessage:           true,
				NeedCompleteConversation: true,
				NeedCocoBot:              true,
				NeedPCPinChain:           true,
				PCPinQueryType:           0,
				ExcludeArchive:           true,
				OnlyArchive:              false,
			},
		}},
	}

	raw, errPost := c.postRaw(ctx, "/im/chain/recent_conv", body)
	if errPost != nil {
		return "", errPost
	}

	var resp conversationListResponse
	if errUnmarshal := json.Unmarshal(raw, &resp); errUnmarshal != nil {
		return "", fmt.Errorf("解析会话列表失败: %w", errUnmarshal)
	}
	if resp.StatusCode != 0 {
		return "", &upstreamError{
			Code:    resp.StatusCode,
			Message: statusCodeHint(resp.StatusCode, resp.StatusDesc),
		}
	}

	for _, cell := range resp.DownlinkBody.PullRecent.Cells {
		if cell.Conversation.ConversationID != "" {
			return cell.Conversation.ConversationID, nil
		}
	}
	return "", nil
}

// postRaw posts an envelope and returns the raw response body.
//
// It differs from post in that it does not require a decodable envelope, which
// matters for list endpoints that answer with a bare object.
func (c *client) postRaw(ctx context.Context, path string, body any) ([]byte, error) {
	raw, errMarshal := json.Marshal(body)
	if errMarshal != nil {
		return nil, errMarshal
	}
	return c.postBytes(ctx, path, raw)
}
