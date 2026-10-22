package main

import (
	"context"
	"strings"
	"time"
)

// 视频结果从会话里补读。
//
// 豆包生成视频时会先推一段文字和参数确认，然后主动断掉 SSE 连接；片段本身要
// 几分钟后才在服务端产出，并且只出现在会话记录里。所以「跟着流读完」这条路
// 对视频永远拿不到结果 —— 连接的结束不代表生成失败。
//
// 正确做法是回头读会话：PULL_SINGLE_CHAIN 把这段会话的消息拉回来，最新一条
// assistant 消息里的 creation 块就带着视频地址。这也解释了为什么以前会
// 「十分钟后什么都没有」：流一断，调用方就收尾了，从未回头看过。

// videoReadbackAttempts 是补读的次数与间隔。
//
// 生成通常 5–10 分钟，前半段会话里还没有成品，太早读只会拿到占位块。
// 15 秒一轮既不会把上游打出限流，也能在结果出现后的半分钟内取到。
const (
	videoReadbackInterval = 15 * time.Second
	// videoReadbackSettleAfterStream 是流断开后的首次等待：此时服务端刚接手，
	// 立刻读必然是空的。
	videoReadbackSettleAfterStream = 20 * time.Second
)

// latestAssistantAssets 读回会话里最新一条 assistant 消息的生成资产。
//
// 返回 assets 与「是否已经拿到成品」：拿到成品才停止补读，否则调用方继续等。
func latestAssistantAssets(ctx context.Context, creds *credentials, conversationID string) ([]creationAsset, bool) {
	if strings.TrimSpace(conversationID) == "" {
		return nil, false
	}
	client := newClient(creds)
	messages, errPull := client.pullChain(ctx, conversationID, 10)
	if errPull != nil {
		return nil, false
	}
	// 从后往前找：最后一个 bot 消息才是这一轮的产物。
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if !msg.isBotReply() {
			continue
		}
		assets := assetsFromStoredMessage(msg)
		if len(assets) == 0 {
			continue
		}
		for _, a := range assets {
			if a.URL != "" {
				return assets, true
			}
		}
		return assets, false
	}
	return nil, false
}

// assetsFromStoredMessage 从一条已入库的消息里取出生成资产。
//
// 注意存储形态与流式事件不同：消息的 content 字段是空串，真正的内容在
// 顶层的 content_block 数组里，块结构与流式那一套一致，所以沿用同一个
// parseCreationBlock。按 content 字符串去解只会拿到空，这是之前读不到
// 视频的原因之一。
func assetsFromStoredMessage(msg conversationMessage) []creationAsset {
	var out []creationAsset
	for _, block := range msg.ContentBlock {
		if block.BlockType != blockTypeCreation {
			continue
		}
		out = append(out, parseCreationBlock(block.Content)...)
	}
	return out
}

// waitForVideoAsset 在流断开之后持续补读，直到拿到视频地址或超时。
//
// 返回最后一次读到的资产（可能只有占位块）与是否成功。
func waitForVideoAsset(ctx context.Context, creds *credentials, conversationID string, budget time.Duration) (creationAsset, bool) {
	if budget <= 0 {
		budget = 15 * time.Minute
	}
	deadline := time.Now().Add(budget)

	timer := time.NewTimer(videoReadbackSettleAfterStream)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return creationAsset{}, false
	case <-timer.C:
	}

	for {
		assets, done := latestAssistantAssets(ctx, creds, conversationID)
		if done {
			for _, a := range assets {
				if a.isVideo() && a.URL != "" {
					return a, true
				}
			}
			// 有成品但不是视频时也认：部分抓包里片段不带 video 标记。
			for _, a := range assets {
				if a.URL != "" {
					return a, true
				}
			}
		}
		if time.Now().After(deadline) {
			// 超时前再读到的内容比什么都没有有用。
			for _, a := range assets {
				if a.URL != "" {
					return a, true
				}
			}
			return creationAsset{}, false
		}

		select {
		case <-ctx.Done():
			return creationAsset{}, false
		case <-time.After(videoReadbackInterval):
		}
	}
}
