package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// 真实会话里的视频块：地址在 download_url，尺寸与时长是字符串。
// 这段 JSON 是从 /im/chain/single 的实际响应里裁出来的。
const realVideoCreationBlock = `{
  "block_type": 2074,
  "content": {
    "creation_block": {
      "creations": [{
        "type": 2,
        "id": "59331063303843586",
        "gen_detail": {"task_type": 6},
        "can_publish": true,
        "video": {
          "vid": "v0269cg10004dbcr6ua7dldfcols5bv0",
          "cover": {
            "image_thumb": {"url": "https://aka.doubaocdn.com/s/rlNorBrMIE", "width": 384, "height": 682}
          },
          "status": 3,
          "width": "384",
          "height": "682",
          "duration": 10.08,
          "video_type": "mp4",
          "download_url": "https://v11-vdl.doubao.com/x/video/tos/cn/a.mp4?download=true"
        }
      }]
    }
  }
}`

// 视频地址必须能从 download_url 取到，且尺寸是字符串时不能把整个解析弄挂。
func TestVideoFromStoredBlock(t *testing.T) {
	var block conversationBlock
	if err := json.Unmarshal([]byte(realVideoCreationBlock), &block); err != nil {
		t.Fatalf("块解析失败: %v", err)
	}
	if block.BlockType != blockTypeCreation {
		t.Fatalf("块类型不对: %d", block.BlockType)
	}

	msg := conversationMessage{UserType: 2, ContentBlock: []conversationBlock{block}}
	assets := assetsFromStoredMessage(msg)
	if len(assets) != 1 {
		t.Fatalf("应解析出 1 个资产，实际 %d", len(assets))
	}
	a := assets[0]
	if !a.isVideo() {
		t.Error("应识别为视频")
	}
	if a.URL == "" || !strings.Contains(a.URL, "v11-vdl.doubao.com") {
		t.Errorf("视频地址没取到: %q", a.URL)
	}
	if a.Width != 384 || a.Height != 682 {
		t.Errorf("尺寸解析错: %dx%d", a.Width, a.Height)
	}
	if a.Duration != 10 {
		t.Errorf("时长解析错: %d", a.Duration)
	}
	if a.Vid != "v0269cg10004dbcr6ua7dldfcols5bv0" {
		t.Errorf("vid 丢失: %q", a.Vid)
	}
}

// 已入库消息的内容在顶层 content_block，content 字段是空串。
// 按 content 字符串去解会得到 0 个资产，这正是之前读不到视频的原因。
func TestStoredContentIsInTopLevelBlocks(t *testing.T) {
	msg := conversationMessage{
		UserType:     2,
		Content:      "",
		ContentBlock: []conversationBlock{{BlockType: blockTypeCreation, Content: json.RawMessage(`{"creation_block":{"creations":[{"type":2,"video":{"download_url":"https://x/a.mp4"}}]}}`)}},
	}
	if got := assetsFromStoredMessage(msg); len(got) != 1 {
		t.Fatalf("顶层 content_block 未被读取: %d", len(got))
	}
	// 只有 content 字符串、没有顶层块时不该崩
	if got := assetsFromStoredMessage(conversationMessage{Content: `{"text":"hi"}`}); len(got) != 0 {
		t.Errorf("纯文本消息不应产生资产: %d", len(got))
	}
}

// 两种数字写法都要能解：豆包给字符串，Dola 给数字。
func TestFlexibleNumberTypes(t *testing.T) {
	for _, raw := range []string{`{"width":"384","height":"682","duration":"10.08"}`,
		`{"width":384,"height":682,"duration":10.08}`} {
		var v creationVideo
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			t.Fatalf("%s 解析失败: %v", raw, err)
		}
		if int(v.Width) != 384 || int(v.Height) != 682 {
			t.Errorf("%s -> %dx%d", raw, int(v.Width), int(v.Height))
		}
		if int(v.Duration*100) != 1008 {
			t.Errorf("%s -> 时长 %v", raw, float64(v.Duration))
		}
	}
}

// 视频块在流中断后要能被 isStreamClosed 认出来，否则每次成功都报错。
func TestStreamClosedDetection(t *testing.T) {
	for _, msg := range []string{
		"unexpected EOF", "read: connection reset by peer",
		"context deadline exceeded", "broken pipe", "stream error",
	} {
		if !isStreamClosed(errString(msg)) {
			t.Errorf("未识别为连接结束: %q", msg)
		}
	}
	for _, msg := range []string{"710020202 common invalid param", "额度不足"} {
		if isStreamClosed(errString(msg)) {
			t.Errorf("误判为连接结束: %q", msg)
		}
	}
	if isStreamClosed(nil) {
		t.Error("nil 不应判定为连接结束")
	}
}

type errString string

func (e errString) Error() string { return string(e) }
