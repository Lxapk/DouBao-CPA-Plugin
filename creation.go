package main

import (
	"encoding/json"
	"strings"
)

// Image and video generation.
//
// A generation request does not return its result as a field. It streams a
// message whose content_block array grows a CREATION block (2074) containing the
// generated asset. The same text channel carries the model's commentary
// alongside it, so a caller has to walk the blocks to find the media.
//
// The block sequence for a successful image generation, as observed:
//
//	10000 (TEXT)         the model narrating
//	10040 (DEEP_THINK)   reasoning, when enabled
//	2074  (CREATION)     the generated image
//
// Within the CREATION block the asset sits at
//
//	content.creation_block.creations[i].image
//
// and progresses from a placeholder (status 1, no key) to the finished asset
// (status 1 with key + image_thumb.url). That progression is why the image has
// to be read from the last block seen rather than the first: an early frame
// carries only the dimensions.
//
// All block type ids come from BlockType.java in the decompiled client.

const (
	blockTypeText       = 10000 // BLOCK_TYPE_TEXT
	blockTypeGenImage   = 10010 // BLOCK_TYPE_GEN_IMAGE
	blockTypeImage      = 10012 // BLOCK_TYPE_IMAGE
	blockTypeCreation   = 2074  // BLOCK_TYPE_CREATION
	blockTypeDeepThink  = 10040 // BLOCK_TYPE_DEEP_THINK
	blockTypeArtifact   = 10030 // BLOCK_TYPE_ARTIFACT
	blockTypeDiaryImage = 10061 // BLOCK_TYPE_DIARY_IMAGE
)

// creationAsset is one generated image or video, resolved to a usable URL.
type creationAsset struct {
	// Type is 1 for an image and 2 for a video.
	Type int
	// ID identifies the creation on the server.
	ID string
	// Key is the content-addressable resource key. It is the input to the
	// no-watermark endpoint and the stable identifier across formats.
	Key string
	// URL is a directly usable image URL, when one was supplied.
	URL string
	// Width and Height are the produced dimensions.
	Width  int
	Height int
	// Model describes the generator, e.g. "Seedream 5.0 Flash".
	Model string
	// Status is the upstream generation status.
	Status int
	// Publishable reports whether the upstream offers the asset for publishing.
	Publishable bool
	// Kinds maps format name to URL for alternate encodings (heic, webp, …).
	Kinds map[string]string
}

// isVideo reports whether the asset is a video.
func (a creationAsset) isVideo() bool { return a.Type == 2 }

// creationBlockPayload mirrors the CREATION block's content.
// creationBlockPayload is the body of a CREATION block's content.
//
// Its shape depends on the caller: a whole-message event nests the payload under
// creation_block, while a patch value already has that layer stripped. Both
// spellings are accepted so one parser serves both call sites.
type creationBlockPayload struct {
	CreationBlock struct {
		Creations []creationItem `json:"creations"`
	} `json:"creation_block"`
	// Creations is the already-unwrapped form.
	Creations []creationItem `json:"creations"`
}

// allCreations returns the creations regardless of which spelling arrived.
func (p *creationBlockPayload) allCreations() []creationItem {
	if len(p.CreationBlock.Creations) > 0 {
		return p.CreationBlock.Creations
	}
	return p.Creations
}

// creationItem is one entry of creations[].
type creationItem struct {
	Type       int             `json:"type"`
	ID         string          `json:"id"`
	GenDetail  json.RawMessage `json:"gen_detail"`
	CanPublish bool            `json:"can_publish"`
	Image      *creationImage  `json:"image"`
	Video      *creationVideo  `json:"video"`
}

// creationImage is the image payload of a creation.
type creationImage struct {
	Status      int                   `json:"status"`
	Key         string                `json:"key"`
	URL         string                `json:"url"`
	ImageThumb  *creationImageVariant `json:"image_thumb"`
	ImageRaw    *creationImageVariant `json:"image_raw"`
	Placeholder *creationPlaceholder  `json:"placeholder"`
}

type creationPlaceholder struct {
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	Description string `json:"description"`
}

// creationImageVariant is one rendered form of the asset.
type creationImageVariant struct {
	URL        string            `json:"url"`
	URI        string            `json:"uri"`
	Width      int               `json:"width"`
	Height     int               `json:"height"`
	URLFormats map[string]string `json:"url_formats"`
}

// creationVideo is the video payload of a creation.
type creationVideo struct {
	Status    int                   `json:"status"`
	Key       string                `json:"key"`
	URL       string                `json:"url"`
	Cover     *creationImageVariant `json:"cover"`
	VideoInfo *creationImageVariant `json:"video_info"`
	Duration  float64               `json:"duration"`
}

// parseCreationBlock extracts every generated asset from a CREATION block body.
func parseCreationBlock(raw json.RawMessage) []creationAsset {
	if len(raw) == 0 {
		return nil
	}
	var payload creationBlockPayload
	if json.Unmarshal(raw, &payload) != nil {
		return nil
	}

	var out []creationAsset
	for _, item := range payload.allCreations() {
		if item.Image != nil {
			if a, ok := assetFromImage(item); ok {
				out = append(out, a)
			}
		}
		if item.Video != nil {
			if a, ok := assetFromVideo(item); ok {
				out = append(out, a)
			}
		}
	}
	return out
}

func assetFromImage(item creationItem) (creationAsset, bool) {
	img := item.Image
	a := creationAsset{
		Type:        item.Type,
		ID:          item.ID,
		Key:         img.Key,
		Status:      img.Status,
		Publishable: item.CanPublish,
	}
	if item.Type == 0 {
		a.Type = 1
	}
	if a.Type == 0 {
		a.Type = 1
	}

	// Prefer the thumbnail: it is a ready-to-use signed URL, whereas key alone
	// needs a second call to resolve.
	if img.ImageThumb != nil {
		a.URL = img.ImageThumb.URL
		a.Kinds = img.ImageThumb.URLFormats
		if a.Width == 0 {
			a.Width = img.ImageThumb.Width
		}
		if a.Height == 0 {
			a.Height = img.ImageThumb.Height
		}
		if a.Key == "" {
			a.Key = img.ImageThumb.URI
		}
	}
	if img.ImageRaw != nil && a.URL == "" {
		a.URL = img.ImageRaw.URL
		a.Kinds = img.ImageRaw.URLFormats
	}
	if a.URL == "" {
		a.URL = img.URL
	}
	if img.Placeholder != nil {
		a.Model = img.Placeholder.Description
		if a.Width == 0 {
			a.Width = img.Placeholder.Width
		}
		if a.Height == 0 {
			a.Height = img.Placeholder.Height
		}
	}

	// An asset with neither a key nor a URL is still a placeholder; there is
	// nothing to hand back yet.
	if a.Key == "" && a.URL == "" {
		return a, false
	}
	return a, true
}

func assetFromVideo(item creationItem) (creationAsset, bool) {
	v := item.Video
	a := creationAsset{
		Type:        2,
		ID:          item.ID,
		Key:         v.Key,
		URL:         v.URL,
		Status:      v.Status,
		Publishable: item.CanPublish,
	}
	if v.Cover != nil {
		// The cover doubles as the poster frame and as the fallback URL when the
		// video itself is not addressable yet.
		if a.URL == "" {
			a.URL = v.Cover.URL
		}
		a.Kinds = v.Cover.URLFormats
		a.Width = v.Cover.Width
		a.Height = v.Cover.Height
	}
	if v.VideoInfo != nil && v.VideoInfo.URL != "" {
		a.URL = v.VideoInfo.URL
	}
	if a.Key == "" && a.URL == "" {
		return a, false
	}
	return a, true
}

// ---------------------------------------------------------------------------
// Response-block decoding
// ---------------------------------------------------------------------------

// responseBlock is one block of a streamed message.
//
// Content is a raw object here rather than a string: the streaming endpoint
// sends it inline, unlike the stored-conversation endpoint which double-encodes
// it. Both shapes are handled by the two parsers in this package.
type responseBlock struct {
	BlockType int             `json:"block_type"`
	BlockID   string          `json:"block_id"`
	Content   json.RawMessage `json:"content"`
	IsFinish  bool            `json:"is_finish"`
	PatchType int             `json:"patch_type"`
}

// blockContent is the union of the block bodies this plugin reads.
type blockContent struct {
	TextBlock     *textBlockContent `json:"text_block"`
	CreationBlock json.RawMessage   `json:"creation_block"`
}

type textBlockContent struct {
	Text string `json:"text"`
}

// streamMessageContent is the content object of a STREAM_MSG_NOTIFY or
// STREAM_CHUNK event.
type streamMessageContent struct {
	ContentBlock  []responseBlock `json:"content_block"`
	ContentStatus int             `json:"content_status"`
	Ext           json.RawMessage `json:"ext"`
}

// textOf concatenates the text blocks of a message.
func (m *streamMessageContent) textOf() string {
	var b strings.Builder
	for i := range m.ContentBlock {
		if m.ContentBlock[i].BlockType != blockTypeText {
			continue
		}
		var c blockContent
		if json.Unmarshal(m.ContentBlock[i].Content, &c) != nil || c.TextBlock == nil {
			continue
		}
		b.WriteString(c.TextBlock.Text)
	}
	return b.String()
}

// creationsOf collects every generated asset across the blocks.
func (m *streamMessageContent) creationsOf() []creationAsset {
	var out []creationAsset
	for i := range m.ContentBlock {
		if m.ContentBlock[i].BlockType != blockTypeCreation {
			continue
		}
		var c blockContent
		if json.Unmarshal(m.ContentBlock[i].Content, &c) != nil {
			continue
		}
		out = append(out, parseCreationBlock(c.CreationBlock)...)
	}
	return out
}

// reasoningOf concatenates the deep-think blocks.
func (m *streamMessageContent) reasoningOf() string {
	var b strings.Builder
	for i := range m.ContentBlock {
		if m.ContentBlock[i].BlockType != blockTypeDeepThink {
			continue
		}
		var c blockContent
		if json.Unmarshal(m.ContentBlock[i].Content, &c) != nil || c.TextBlock == nil {
			continue
		}
		b.WriteString(c.TextBlock.Text)
	}
	return b.String()
}
