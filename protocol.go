package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// This file is the wire protocol: the envelope, the command ids, the content
// types and the query string every IM call carries.
//
// Sources, all verified against the live API:
//
//	Content-Type   ov2/a.java:66  -> "application/json; encoding=utf-8"
//	envelope       com/larus/im/internal/protocol/bean/UplinkMessage.java
//	command ids    com/larus/im/internal/protocol/bean/IMCMD.java
//	content types  com/larus/im/internal/jni/bean/OmniMessageContentType.java
//	conv types     com/larus/im/internal/jni/bean/OmniConversationType.java
//
// The transport is JSON over HTTP, not protobuf. The /im/* family looks like a
// protobuf API when probed without the right header (the gateway answers
// 712012002 and, for some content types, emits a protobuf error frame), which
// is a red herring: once Content-Type carries "encoding=utf-8" the same
// endpoints speak plain JSON in both directions.

// contentTypeJSON is the only Content-Type the IM gateway accepts.
//
// The value is unusual: the parameter is "encoding", not the standard "charset".
// Anything else — including "application/json;charset=UTF-8" — is rejected with
// 712012002 "不支持编码类型" before the body is even parsed.
const contentTypeJSON = "application/json; encoding=utf-8"

// IM command ids, from IMCMD.java.
const (
	cmdSendMessage      = 100  // SEND_MESSAGE
	cmdSendMessageList  = 110  // SEND_MESSAGE_LIST
	cmdFetchChunk       = 300  // FETCH_CHUNK_MESSAGE
	cmdConversationInfo = 1110 // GET_CONVERSATION_INFO
	cmdMarkConvRead     = 2100 // MARK_CONVERSATION_READ
	cmdRegenerate       = 2230 // REGENERATE_MSG
	cmdBreakStream      = 2240 // BREAK_MSG
	cmdPullSingleChain  = 3100 // PULL_SINGLE_CHAIN
	cmdPullRecentConv   = 3200 // PULL_RECENT_CONV_CHAIN
)

// Message content types, from OmniMessageContentType.
const (
	contentTypeText = 1 // TXT
)

// Conversation types, from OmniConversationType.
const (
	conversationTypeOneToOneBot = 3 // ONE_TO_BOT_CHAT
)

// uplinkMessage is the request envelope.
//
// The field names are the serial names kotlinx.serialization writes; they are
// snake_case and must be reproduced exactly.
type uplinkMessage struct {
	Cmd        int        `json:"cmd"`
	Channel    int        `json:"channel"`
	SequenceID string     `json:"sequence_id"`
	UplinkBody uplinkBody `json:"uplink_body"`
	Version    string     `json:"version"`
}

// uplinkBody carries exactly one command payload. The JSON tags are the serial
// names observed in the APK's UplinkBody serializer.
type uplinkBody struct {
	SendMessageBody       *sendMessageBody       `json:"send_message_body,omitempty"`
	FetchChunkMessageBody *fetchChunkMessageBody `json:"fetch_chunk_message_uplink_body,omitempty"`
	PullSingleChainBody   *pullSingleChainBody   `json:"pull_singe_chain_uplink_body,omitempty"`
	MarkConvReadBody      *markConvReadBody      `json:"mark_conv_read_uplink_body,omitempty"`
	RegenerateMsgBody     *regenerateMsgBody     `json:"regenerate_msg_uplink_body,omitempty"`
	BreakStreamMsgBody    *breakStreamMsgBody    `json:"break_stream_msg_uplink_body,omitempty"`
	PullRecentConvBody    *pullRecentConvBody    `json:"pull_recent_conv_chain_uplink_body,omitempty"`
}

// pullRecentConvBody lists the account's recent conversations.
//
// It is how the plugin obtains a usable conversation id, because
// /chat/completion rejects a placeholder.
type pullRecentConvBody struct {
	Limit               int                  `json:"limit"`
	MessageCountPerConv int                  `json:"message_count_per_conv"`
	APIVersion          int                  `json:"api_version"`
	ConvVersion         int                  `json:"conv_version"`
	Direction           int                  `json:"direction"`
	Option              pullRecentConvOption `json:"option"`
}

type pullRecentConvOption struct {
	NotNeedMessage           bool `json:"not_need_message"`
	NeedCompleteConversation bool `json:"need_complete_conversation"`
	NeedCocoBot              bool `json:"need_coco_bot"`
	NeedPCPinChain           bool `json:"need_pc_pin_chain"`
	PCPinQueryType           int  `json:"pc_pin_query_type"`
	ExcludeArchive           bool `json:"exclude_archive"`
	OnlyArchive              bool `json:"only_archive"`
}

// sendMessageBody is the chat send payload.
//
// Field names come from SendMessageUplinkBody's serializer descriptor. Only the
// fields a text chat needs are modelled; the rest (attachments, cards, applets)
// are omitted rather than sent as zero values, because the gateway rejects
// unknown-but-present fields less predictably than absent ones.
type sendMessageBody struct {
	ConversationID      string        `json:"conversation_id"`
	ConversationType    int           `json:"conversation_type"`
	LocalConversationID string        `json:"local_conversation_id"`
	LocalMessageID      string        `json:"local_message_id"`
	BotID               string        `json:"bot_id"`
	ContentType         int           `json:"content_type"`
	Content             string        `json:"content"`
	SenderID            string        `json:"sender_id"`
	CreateTime          int64         `json:"create_time"`
	Status              int           `json:"status"`
	Skill               *messageSkill `json:"client_controller_param,omitempty"`
}

// messageSkill activates an upstream capability for one message.
//
// It rides in client_controller_param — the field the client itself uses to say
// "this turn is an image generation", rather than ordinary chat.
type messageSkill struct {
	SkillID int `json:"skill_id"`
}

// pullSingleChainBody fetches conversation history. It is also how a reply is
// read back after sending.
type pullSingleChainBody struct {
	ConversationID   string `json:"conversation_id"`
	ConversationType int    `json:"conversation_type"`
	Direction        int    `json:"direction"`
	Limit            int    `json:"limit"`
	IndexInConv      string `json:"index_in_conv,omitempty"`
}

// fetchChunkMessageBody pulls a streaming reply by its token.
//
// This is the second half of the two-step send: /im/send/message returns an
// sse_token, and this command drains the model's chunks for it.
type fetchChunkMessageBody struct {
	Token            string `json:"token"`
	SeqSt            int    `json:"seq_st"`
	AcceptBinaryType int    `json:"accept_binary_type"`
}

type markConvReadBody struct {
	ConversationID   string `json:"conversation_id"`
	ConversationType int    `json:"conversation_type"`
}

type regenerateMsgBody struct {
	ConversationID   string `json:"conversation_id"`
	ConversationType int    `json:"conversation_type"`
	MessageID        string `json:"message_id"`
}

type breakStreamMsgBody struct {
	ConversationID   string `json:"conversation_id"`
	ConversationType int    `json:"conversation_type"`
	MessageID        string `json:"message_id"`
}

// downlinkMessage is the response envelope.
type downlinkMessage struct {
	Cmd          int             `json:"cmd"`
	SequenceID   string          `json:"sequence_id"`
	DownlinkBody json.RawMessage `json:"downlink_body"`
	Version      string          `json:"version"`
	StatusCode   int             `json:"status_code"`
	StatusDesc   string          `json:"status_desc"`
	Ext          json.RawMessage `json:"ext"`
}

// ok reports whether the gateway accepted the command.
func (d *downlinkMessage) ok() bool { return d.StatusCode == 0 }

// sendMessageAck is the reply to SEND_MESSAGE.
type sendMessageAck struct {
	ConversationID   string `json:"conversation_id"`
	ConversationType int    `json:"conversation_type"`
	MessageID        string `json:"message_id"`
	LocalMessageID   string `json:"local_message_id"`
	LastSectionID    string `json:"last_section_id"`
	MessageIndex     int64  `json:"message_index"`
	AckNextAction    struct {
		AckNextAction   int `json:"ack_next_action"`
		BotReplyTimeOut int `json:"BotReplyTimeOut"`
	} `json:"ack_next_action"`
	TokenMixed struct {
		SSEToken       string `json:"sse_token"`
		ReplyUniqueKey string `json:"reply_unique_key"`
	} `json:"token_mixed"`
}

// pullSingleChainAck is the reply to PULL_SINGLE_CHAIN.
type pullSingleChainAck struct {
	Messages []conversationMessage `json:"messages"`
}

// conversationMessage is one stored message.
//
// It is used by the legacy polling path (pullChain). The streaming path decodes
// the same payload through responseBlock instead, because there the content
// blocks arrive as inline objects rather than as an encoded string.
type conversationMessage struct {
	ConversationID string          `json:"conversation_id"`
	MessageID      string          `json:"message_id"`
	SenderID       string          `json:"sender_id"`
	UserType       int             `json:"user_type"`
	Status         int             `json:"status"`
	ContentType    int             `json:"content_type"`
	Content        string          `json:"content"`
	IndexInConv    string          `json:"index_in_conv"`
	CreateTime     string          `json:"create_time"`
	Ext            json.RawMessage `json:"ext"`
}

// isBotReply reports whether the message came from the assistant.
// user_type 2 marks the model.
func (m *conversationMessage) isBotReply() bool { return m.UserType == 2 }

// messageText unwraps the JSON-encoded content into plain text.
//
// Content is `{"text":"..."}`. A message that is not text decodes to an empty
// string rather than an error, because callers want "the text if there is any".
func (m *conversationMessage) messageText() string {
	if m.Content == "" {
		return ""
	}
	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(m.Content), &payload); err != nil {
		return strings.TrimSpace(m.Content)
	}
	return payload.Text
}

// messageTextPayload encodes the inner content object.
func messageTextPayload(text string) (string, error) {
	raw, errMarshal := json.Marshal(map[string]string{"text": text})
	if errMarshal != nil {
		return "", errMarshal
	}
	return string(raw), nil
}

// buildQuery assembles the query string every IM call carries.
//
// The gateway validates the parameter set, so the whole block is sent even
// though most of it is constant. Notably absent: msToken and a_bogus. Both
// appear in the browser's own requests, but neither is required — the endpoints
// used here accept calls without them, which removes any need to reimplement
// the signature algorithm.
func buildQuery(c *credentials, extra map[string]string) string {
	p := c.profile()
	q := url.Values{}
	q.Set("version_code", doubaoVersionCode)
	q.Set("language", p.Locale)
	q.Set("device_platform", "web")
	q.Set("doubao_device_platform", "web")
	q.Set("aid", p.AID)
	q.Set("real_aid", p.AID)
	q.Set("pkg_type", "release_version")
	q.Set("device_id", c.DeviceID)
	q.Set("pc_version", p.PCCVersion)
	q.Set("doubao_pc_version", p.PCCVersion)
	q.Set("web_id", c.WebID)
	q.Set("tea_uuid", c.WebID)
	q.Set("region", p.Region)
	q.Set("sys_region", p.SysRegion)
	q.Set("samantha_web", "1")
	q.Set("web_platform", "browser")
	q.Set("use-olympus-account", "1")
	q.Set("web_tab_id", c.WebTabID)
	q.Set("flow_im_arch", "v2")
	for k, v := range extra {
		q.Set(k, v)
	}
	return q.Encode()
}

// endpointURL builds a full URL for an IM path on this credential's realm.
func endpointURL(c *credentials, path string, extra map[string]string) string {
	return strings.TrimRight(c.profile().Host, "/") + path + "?" + buildQuery(c, extra)
}

// realmDefaultBot is the assistant id used when a credential does not name one.
//
// The web client reports this from /alice/user/launch (assistant_bot_id). The
// value is stable per realm, so it is baked in as a fallback and refreshed from
// the launch response whenever a credential is validated.
var realmDefaultBot = map[realm]string{
	realmDoubao: "7338286299411103781",
	realmDola:   "7339470689562525703",
}

func defaultBotIDFor(r realm) string {
	if id, ok := realmDefaultBot[r]; ok {
		return id
	}
	return realmDefaultBot[realmDoubao]
}

// statusCodeHint turns a gateway status code into advice for the operator.
//
// The codes are the ones the gateway actually returns on this path; the
// messages name what to check rather than restating the number.
func statusCodeHint(code int, desc string) string {
	switch code {
	case 0:
		return ""
	case 710012001:
		return "登录态已失效，请重新复制 Cookie（LoginInvalid）"
	case 710022004, 710022005:
		return "上游限流，请稍后重试"
	case 710020202:
		return "请求参数不被接受：请确认 Content-Type 为 application/json; encoding=utf-8"
	case 712012002:
		return "网关拒绝了编码类型：Content-Type 必须是 application/json; encoding=utf-8"
	case 712012003:
		return "网关不支持该 cmd：请确认请求体中的 cmd 取值"
	case 710010202:
		return "上游返回系统错误：通常是 Cookie 与 device_id 不匹配，建议重新授权"
	}
	if desc == "" {
		desc = "未知错误"
	}
	return fmt.Sprintf("上游错误 %d：%s", code, desc)
}
