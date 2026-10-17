//go:build windows

package collect

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/tglobalcorp-mis/nexmis/internal/protocol"
)

var (
	modwevtapi = windows.NewLazySystemDLL("wevtapi.dll")

	procEvtQuery                 = modwevtapi.NewProc("EvtQuery")
	procEvtNext                  = modwevtapi.NewProc("EvtNext")
	procEvtClose                 = modwevtapi.NewProc("EvtClose")
	procEvtCreateRenderContext   = modwevtapi.NewProc("EvtCreateRenderContext")
	procEvtRender                = modwevtapi.NewProc("EvtRender")
	procEvtOpenPublisherMetadata = modwevtapi.NewProc("EvtOpenPublisherMetadata")
	procEvtFormatMessage         = modwevtapi.NewProc("EvtFormatMessage")
)

const (
	evtQueryChannelPath      = 0x1
	evtQueryReverseDirection = 0x200
	evtRenderContextSystem   = 1
	evtRenderEventValues     = 0
	evtFormatMessageEvent    = 1

	// EVT_SYSTEM_PROPERTY_ID indices.
	evtSystemProviderName  = 0
	evtSystemEventID       = 2
	evtSystemTimeCreated   = 8
	evtSystemPropertyIDEnd = 18

	// EVT_VARIANT_TYPE values.
	evtVarTypeString   = 1
	evtVarTypeUInt16   = 6
	evtVarTypeFileTime = 17

	evtVariantSize = 16 // union (8) + Count (4) + Type (4)

	eventXPath          = "*[System[(Level=1 or Level=2) and TimeCreated[timediff(@SystemTime) <= 86400000]]]"
	maxEventsPerLog     = 20000
	maxGroupsWithHandle = 256
	topEventGroups      = 10
	eventMessageRunes   = 300
	eventLogBudget      = 15 * time.Second
)

var eventLogs = []string{"System", "Application"}

func evtClose(h uintptr) {
	if h != 0 {
		procEvtClose.Call(h)
	}
}

type eventGroup struct {
	sample protocol.EventSample
	handle uintptr // most recent event, kept for message formatting
}

// recentEventErrors counts critical/error events of the last 24 h in the
// System and Application logs and returns the top groups by Source+EventID.
func (c *windowsCollector) recentEventErrors(ctx context.Context) (int, []protocol.EventSample) {
	if err := modwevtapi.Load(); err != nil {
		c.warn("wevtapi", err)
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(ctx, eventLogBudget)
	defer cancel()

	renderCtx, _, e := procEvtCreateRenderContext.Call(0, 0, evtRenderContextSystem)
	if renderCtx == 0 {
		c.warn("EvtCreateRenderContext", e)
		return 0, nil
	}
	defer evtClose(renderCtx)

	groups := map[string]*eventGroup{}
	kept := 0
	defer func() {
		for _, g := range groups {
			evtClose(g.handle)
		}
	}()

	total := 0
	renderBuf := make([]uint64, 512) // 4 KiB, 8-byte aligned
	for _, logName := range eventLogs {
		q, _, e := procEvtQuery.Call(0,
			uintptr(unsafe.Pointer(utf16Ptr(logName))),
			uintptr(unsafe.Pointer(utf16Ptr(eventXPath))),
			evtQueryChannelPath|evtQueryReverseDirection)
		if q == 0 {
			c.warn("EvtQuery "+logName, e)
			continue
		}
		n := 0
		var handles [64]uintptr
	batches:
		for n < maxEventsPerLog && ctx.Err() == nil {
			var returned uint32
			r, _, e := procEvtNext.Call(q, uintptr(len(handles)), uintptr(unsafe.Pointer(&handles[0])),
				2000, 0, uintptr(unsafe.Pointer(&returned)))
			if r == 0 {
				if !errors.Is(e, windows.ERROR_NO_MORE_ITEMS) && !errors.Is(e, windows.ERROR_TIMEOUT) {
					c.warn("EvtNext "+logName, e)
				}
				break batches
			}
			for i := uint32(0); i < returned; i++ {
				h := handles[i]
				n++
				total++
				provider, id, at, ok := renderSystem(renderCtx, h, &renderBuf)
				if !ok {
					evtClose(h)
					continue
				}
				key := logName + "\x00" + provider + "\x00" + strconv.Itoa(id)
				g := groups[key]
				if g == nil {
					g = &eventGroup{sample: protocol.EventSample{Log: logName, Source: provider, EventID: id}}
					groups[key] = g
				}
				g.sample.Count++
				if at.After(g.sample.LastTime) {
					g.sample.LastTime = at
					if g.handle == 0 && kept < maxGroupsWithHandle {
						g.handle = h
						kept++
						continue
					}
				}
				evtClose(h)
			}
		}
		evtClose(q)
	}

	list := make([]*eventGroup, 0, len(groups))
	for _, g := range groups {
		list = append(list, g)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].sample.Count != list[j].sample.Count {
			return list[i].sample.Count > list[j].sample.Count
		}
		return list[i].sample.LastTime.After(list[j].sample.LastTime)
	})
	if len(list) > topEventGroups {
		list = list[:topEventGroups]
	}
	out := make([]protocol.EventSample, 0, len(list))
	for _, g := range list {
		if g.handle != 0 && ctx.Err() == nil {
			g.sample.Message = truncateRunes(cleanString(formatEventMessage(g.sample.Source, g.handle)), eventMessageRunes)
		}
		out = append(out, g.sample)
	}
	return total, out
}

// renderSystem extracts provider, event ID and time from an event.
func renderSystem(renderCtx, event uintptr, buf *[]uint64) (string, int, time.Time, bool) {
	var used, props uint32
	for attempt := 0; attempt < 2; attempt++ {
		b := *buf
		r, _, e := procEvtRender.Call(renderCtx, event, evtRenderEventValues,
			uintptr(len(b)*8), uintptr(unsafe.Pointer(&b[0])),
			uintptr(unsafe.Pointer(&used)), uintptr(unsafe.Pointer(&props)))
		if r != 0 {
			break
		}
		if errors.Is(e, windows.ERROR_INSUFFICIENT_BUFFER) && attempt == 0 {
			*buf = make([]uint64, used/8+1)
			continue
		}
		return "", 0, time.Time{}, false
	}
	if props < evtSystemTimeCreated+1 {
		return "", 0, time.Time{}, false
	}
	base := unsafe.Pointer(&(*buf)[0])
	variant := func(i int) (val uint64, typ uint32) {
		p := unsafe.Add(base, i*evtVariantSize)
		return *(*uint64)(p), *(*uint32)(unsafe.Add(p, 12))
	}
	var provider string
	if _, t := variant(evtSystemProviderName); t == evtVarTypeString {
		if sp := *(**uint16)(unsafe.Add(base, evtSystemProviderName*evtVariantSize)); sp != nil {
			provider = windows.UTF16PtrToString(sp)
		}
	}
	var id int
	if v, t := variant(evtSystemEventID); t == evtVarTypeUInt16 {
		id = int(uint16(v))
	}
	var at time.Time
	if v, t := variant(evtSystemTimeCreated); t == evtVarTypeFileTime {
		at = filetimeToTime(v)
	}
	return provider, id, at, true
}

// formatEventMessage renders the localized message of an event; "" when
// the provider's message resources are unavailable.
func formatEventMessage(provider string, event uintptr) string {
	if provider == "" {
		return ""
	}
	meta, _, _ := procEvtOpenPublisherMetadata.Call(0, uintptr(unsafe.Pointer(utf16Ptr(provider))), 0, 0, 0)
	if meta == 0 {
		return ""
	}
	defer evtClose(meta)
	buf := make([]uint16, 2048)
	var used uint32
	for attempt := 0; attempt < 2; attempt++ {
		r, _, e := procEvtFormatMessage.Call(meta, event, 0, 0, 0, evtFormatMessageEvent,
			uintptr(len(buf)), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&used)))
		if r != 0 {
			return windows.UTF16ToString(buf)
		}
		if errors.Is(e, windows.ERROR_INSUFFICIENT_BUFFER) && attempt == 0 && used > 0 && used < 1<<20 {
			buf = make([]uint16, used)
			continue
		}
		// ERROR_EVT_UNRESOLVED_* still produce a usable partial message.
		if used > 0 && used <= uint32(len(buf)) {
			return windows.UTF16ToString(buf[:used])
		}
		return ""
	}
	return ""
}
