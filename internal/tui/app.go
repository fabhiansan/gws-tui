package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/fabhiansan/gws-tui/internal/api"
	"github.com/fabhiansan/gws-tui/internal/tui/theme"
)

type pane int

const (
	paneList pane = iota
	paneDetail
	paneAction
	// paneMailSidebar is the Gmail-style folder rail. It only exists in the
	// Mail feature, where it sits left of the inbox list.
	paneMailSidebar
)

func New(opts Options) Model {
	ctx, cancel := context.WithCancel(context.Background())
	cfg := opts.Config
	cfg.InitialFeature = normalizeFeature(cfg.InitialFeature)
	persisted := loadPersistedState(cfg.StatePath)
	cache := newWorkspaceCache()
	cacheLoaded := false
	if opts.InitialSnapshot != nil {
		cache = *opts.InitialSnapshot
		cache.EnsureMaps()
		cacheLoaded = cache.HasData()
	} else if !cfg.Daemon {
		cache, cacheLoaded = loadWorkspaceCache(cfg.CachePath)
	}
	feature := Feature(cfg.InitialFeature)
	if persisted.LastFeature != "" && cfg.InitialFeature == "chat" {
		feature = Feature(normalizeFeature(persisted.LastFeature))
	}

	spin := spinner.New()
	spin.Spinner = spinner.Dot

	input := textarea.New()
	input.Placeholder = "message"
	input.SetHeight(3)
	input.ShowLineNumbers = false
	input.Blur()

	detail := viewport.New(80, 20)

	model := Model{
		client:         opts.Client,
		cfg:            cfg,
		theme:          theme.New(cfg.Theme, cfg.NoColor),
		ctx:            ctx,
		cancel:         cancel,
		feature:        feature,
		authRequired:   opts.ForceAuth,
		upstreamHint:   opts.UpstreamHint,
		version:        opts.Version,
		commit:         opts.Commit,
		buildDate:      opts.BuildDate,
		spinner:        spin,
		input:          input,
		detail:         detail,
		focusedPane:    paneList,
		mailFolder:     defaultMailFolder,
		calendarView:   calViewMonth,
		calendarCursor: startOfDay(time.Now()),
		loading:        false,
		featureLoading: map[Feature]bool{},
		featureLoaded:  map[Feature]bool{},
		chatLoadIDs:    map[string]int{},
		seenMessages:   map[string]bool{},
		userLabels:     map[string]string{},
		pendingUsers:   map[string]bool{},
		membersBySpace: map[string][]api.SpaceMember{},
		pendingMembers: map[string]bool{},
		selfUserIDs:    map[string]bool{},
		senderColorIdx: map[string]int{},
		selected: map[Feature]int{
			FeatureChat:     persisted.Selections[string(FeatureChat)],
			FeatureMail:     persisted.Selections[string(FeatureMail)],
			FeatureCalendar: persisted.Selections[string(FeatureCalendar)],
			FeatureMeet:     persisted.Selections[string(FeatureMeet)],
			FeatureTasks:    persisted.Selections[string(FeatureTasks)],
			FeatureDrive:    persisted.Selections[string(FeatureDrive)],
			FeatureDocs:     persisted.Selections[string(FeatureDocs)],
		},
		persisted:          persisted,
		cache:              cache,
		cacheLoaded:        cacheLoaded,
		imageFiles:         map[string]string{},
		imageLoading:       map[string]bool{},
		imageErrors:        map[string]string{},
		imageRenders:       map[string]inlineImageRender{},
		imageFramePend:     map[string]bool{},
		detailImageAt:      map[int]api.Attachment{},
		detailAttachmentAt: map[int]api.Attachment{},
		detailMessageAt:    map[int]string{},
		chatReactions:      map[string]string{},
	}
	if cacheLoaded {
		model.hydrateWorkspaceCache(cache)
		for _, f := range featureOrder {
			model.featureLoaded[f] = true
		}
	} else {
		// Drive and Docs are deliberately omitted — they load lazily on first
		// visit so the cold-start fan-out stays small.
		for _, f := range startupFeatures {
			model.featureLoading[f] = true
		}
	}
	return model
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.spinner.Tick, m.autosaveTick()}
	cmds = append(cmds, m.imageDownloadCmdsForWorkspace()...)
	if !m.cacheLoaded {
		// Cold start: the TUI opens immediately and each section fetches in
		// its own goroutine via tea.Batch, so panes fill in progressively as
		// data arrives instead of blocking on a single sequential load.
		cmds = append(cmds, m.whoamiCmd())
		cmds = append(cmds, m.startupLoadCmds()...)
	} else {
		cmds = append(cmds, m.whoamiCmd())
		cmds = append(cmds, m.enrichSpacesCmds()...)
		cmds = append(cmds, m.enrichSendersCmds()...)
		if m.cfg.Daemon {
			cmds = append(cmds, m.subscribeCmd())
		}
	}
	if m.cfg.Daemon {
		cmds = append(cmds, m.daemonEventCmd())
	}
	if m.cacheLoaded && m.feature == FeatureCalendar && m.calendarView == calViewMonth && !monthStart(m.calendarCursorOrToday()).Equal(m.calendarMonth) {
		cmds = append(cmds, m.calendarMonthCmd(m.calendarCursorOrToday()))
	}
	return tea.Batch(cmds...)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	prevFeature := m.feature
	prevFocus := m.focusedPane
	prevErr := m.err
	prevToast := m.toast
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resize()
		m.resizeModalFields()
		cmds = append(cmds, m.precomputeFrameCmdsForCurrentDetail()...)
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)
	case authLoadedMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
		}
		m.auth = msg.auth
		if !msg.auth.Valid() {
			m.authRequired = true
		}
	case chatSectionLoadedMsg:
		cmds = append(cmds, m.handleChatSectionLoaded(msg)...)
	case loadedMsg:
		cmds = append(cmds, m.handleLoaded(msg)...)
	case chatLoadedMsg:
		cmds = append(cmds, m.handleChatLoaded(msg)...)
	case featureLoadedMsg:
		cmds = append(cmds, m.handleFeatureLoaded(msg)...)
	case featureRefreshedMsg:
		cmds = append(cmds, m.handleFeatureRefreshed(msg)...)
	case chatSentMsg:
		cmds = append(cmds, m.handleChatSent(msg)...)
	case chatEditedMsg:
		cmds = append(cmds, m.handleChatEdited(msg)...)
	case chatDeletedMsg:
		cmds = append(cmds, m.handleChatDeleted(msg)...)
	case chatSpaceCreatedMsg:
		cmds = append(cmds, m.handleChatSpaceCreated(msg)...)
	case chatReactionMsg:
		cmds = append(cmds, m.handleChatReaction(msg)...)
	case mailActionMsg:
		cmds = append(cmds, m.handleMailAction(msg)...)
	case mailDraftActionMsg:
		cmds = append(cmds, m.handleMailDraftAction(msg)...)
	case eventActionMsg:
		cmds = append(cmds, m.handleEventAction(msg)...)
	case calendarMonthLoadedMsg:
		cmds = append(cmds, m.handleCalendarMonthLoaded(msg)...)
	case meetActionMsg:
		cmds = append(cmds, m.handleMeetAction(msg)...)
	case taskActionMsg:
		cmds = append(cmds, m.handleTaskAction(msg)...)
	case pinActionMsg:
		if msg.err != nil {
			// Roll back the optimistic local flip so the indicator
			// matches what the daemon actually has.
			for i := range m.spaces {
				if m.spaces[i].Name == msg.space {
					m.spaces[i].Live = !msg.pinned
					break
				}
			}
			m.err = msg.err.Error()
		}
	case realtimeMsg:
		cmds = append(cmds, m.handleRealtime(msg)...)
	case imageCachedMsg:
		delete(m.imageLoading, msg.source)
		if msg.err == nil && msg.source != "" && msg.path != "" {
			m.imageFiles[msg.source] = msg.path
			delete(m.imageErrors, msg.source)
			m.imageVersion++
			cmds = append(cmds, m.precomputeFrameCmdsForCurrentDetail()...)
		} else if msg.err != nil && msg.source != "" {
			m.imageErrors[msg.source] = msg.err.Error()
			m.imageVersion++
		}
	case imageFrameReadyMsg:
		delete(m.imageFramePend, msg.key)
		if msg.err == nil && msg.key != "" {
			m.imageRenders[msg.key] = inlineImageRender{
				file:        msg.file,
				source:      msg.source,
				columns:     msg.columns,
				rows:        msg.rows,
				size:        msg.size,
				modTime:     msg.modTime,
				full:        msg.full,
				placeholder: msg.placeholder,
			}
			m.imagePlacement++
			m.imageVersion++
		}
	case attachmentDownloadedMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
			break
		}
		if msg.path != "" {
			m.toast = "downloaded to " + compactHomePath(msg.path)
		}
	case docLoadedMsg:
		cmds = append(cmds, m.handleDocLoaded(msg)...)
	case daemonEventMsg:
		cmds = append(cmds, m.handleDaemonEvent(msg)...)
	case selfResolvedMsg:
		cmds = append(cmds, m.handleSelfResolved(msg)...)
	case userResolvedMsg:
		cmds = append(cmds, m.handleUserResolved(msg)...)
	case membersLoadedMsg:
		cmds = append(cmds, m.handleMembersLoaded(msg)...)
	case autosaveMsg:
		if m.modal != nil && m.modal.autosave {
			if err := m.saveDraft(); err == nil {
				m.modal.savedAt = time.Now()
			}
		}
		cmds = append(cmds, m.autosaveTick())
	case imageViewerOpenErrMsg:
		m.toast = "open: " + msg.err
	case detailURLOpenErrMsg:
		m.toast = "open: " + msg.err
	case tea.MouseMsg:
		next, cmd := m.updateMouse(msg)
		m = next
		cmds = append(cmds, cmd)
	case tea.KeyMsg:
		if m.modal != nil {
			next, cmd := m.updateModal(msg)
			m = next
			cmds = append(cmds, cmd)
			break
		}
		next, cmd := m.updateKey(msg)
		m = next
		cmds = append(cmds, cmd)
	}

	// The folder rail only exists in the Mail feature; if focus is left on it
	// while switching away, fall back to the list pane so j/k keep working.
	if m.feature != FeatureMail && m.focusedPane == paneMailSidebar {
		m.focusedPane = paneList
	}
	if m.feature == FeatureDocs {
		if prevFeature != FeatureDocs || m.focusedPane == paneAction {
			m.focusedPane = paneList
		}
	}
	// Meet renders single-pane, so it never has a detail or action pane to
	// focus; keep focus on the list so j/k always move the conference cursor.
	if m.feature == FeatureMeet {
		m.focusedPane = paneList
	}

	// Mail uses a different pane layout than the other features, and its
	// composer pane appears only while focused — so the viewport sizes have
	// to be recomputed whenever the feature or the focused pane changes.
	if m.feature != prevFeature || m.focusedPane != prevFocus {
		m.resize()
	}
	// Drive and Docs aren't fetched at startup; the first time the user opens
	// one, kick off its load now.
	if m.feature != prevFeature {
		if m.feature == FeatureCalendar && m.calendarView == calViewMonth {
			var cmd tea.Cmd
			m, cmd = m.ensureCalendarMonth(m.calendarCursorOrToday())
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		} else if cmd := m.ensureFeatureLoadedCmd(m.feature); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	cmds = append(cmds, m.imageDownloadCmdsForCurrentDetail()...)
	cmds = append(cmds, m.precomputeFrameCmdsForCurrentDetail()...)
	m.updateDetailContent()
	m.captureTransientMessages(prevErr, prevToast)
	return m, tea.Batch(cmds...)
}

func (m Model) updateKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	if m.imageViewer != nil {
		return m.updateImageViewer(msg)
	}
	if m.messagesVisible {
		if msg.String() == "ctrl+c" {
			m.persist()
			m.cancel()
			return m, tea.Quit
		}
		return m.updateMessageLogKey(msg), nil
	}
	if m.helpVisible {
		key := msg.String()
		switch key {
		case "?", "esc", "q":
			m.helpVisible = false
			m.helpScroll = 0
		default:
			m.updateHelpScroll(key)
		}
		return m, nil
	}
	if m.spaceFilterActive && m.feature == FeatureChat && m.focusedPane == paneList {
		return m.updateSpaceFilter(msg)
	}
	if m.feature == FeatureChat && msg.String() == "ctrl+v" {
		return m.handleChatPaste()
	}
	if m.feature == FeatureChat && msg.String() == "ctrl+x" && len(m.pendingChatAttachments) > 0 {
		return m.clearPendingChatAttachments(), nil
	}
	if m.focusedPane == paneAction {
		if m.cfg.VimMode {
			key := msg.String()
			if m.vimComposer == vimModeNormal && key == "esc" {
				m.focusedPane = paneList
				m.input.Blur()
				m.vimPending = ""
				m.clearReplyContext()
				return m, nil
			}
			if m.vimComposer == vimModeNormal && key == "enter" {
				return m.submitAction()
			}
			if m.vimComposer == vimModeNormal && key == "/" {
				m.openSearchModal()
				return m, nil
			}
			if m.vimComposer == vimModeNormal && m.vimPending == "" {
				switch key {
				case "1":
					if m.feature == FeatureMail {
						m.focusMailSidebar()
					} else {
						m.focusedPane = paneList
					}
					m.input.Blur()
					return m, nil
				case "2":
					m.focusedPane = paneDetail
					m.input.Blur()
					return m, nil
				case "3":
					return m, nil
				}
			}
			if m.vimComposerKey(msg) {
				return m, nil
			}
		} else if msg.String() == "esc" {
			m.focusedPane = paneList
			m.input.Blur()
			m.clearReplyContext()
			return m, nil
		}
		switch msg.String() {
		case "enter":
			return m.submitAction()
		case "shift+enter":
			m.input.InsertString("\n")
			return m, nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}

	if m.focusedPane == paneDetail && m.cfg.VimMode {
		if next, cmd, handled := m.updateDetailVim(msg); handled {
			return next, cmd
		}
	}

	// The month grid remaps the list-pane navigation keys to day/week/month
	// movement; everything it does not claim falls through to the switch
	// below so global keys (Tab, Ctrl+N, ?, …) keep working.
	if m.feature == FeatureCalendar && m.calendarView == calViewMonth && m.focusedPane == paneList {
		if next, cmd, handled := m.updateCalendarMonthKey(msg); handled {
			return next, cmd
		}
	}

	switch msg.String() {
	case "ctrl+c", "q":
		m.persist()
		m.cancel()
		return m, tea.Quit
	case "?":
		m.helpVisible = true
		m.helpScroll = 0
		m.helpPending = ""
		return m, nil
	case ":":
		m.openMessageLog()
		return m, nil
	case "H", "ctrl+h":
		// In Mail the folder rail sits left of the list, so H steps left
		// into it once the list is already focused.
		if m.feature == FeatureMail && m.focusedPane == paneList {
			m.focusMailSidebar()
			return m, nil
		}
		m.focusedPane = paneList
		return m, nil
	case "L", "ctrl+l":
		if m.feature == FeatureMail && m.focusedPane == paneMailSidebar {
			m.focusedPane = paneList
			return m, nil
		}
		if m.feature == FeatureDocs {
			return m.openSelectedDoc()
		}
		if m.feature == FeatureMeet {
			return m, nil
		}
		m.focusedPane = paneDetail
		return m, nil
	case "esc":
		if m.focusedPane != paneList {
			m.focusedPane = paneList
			return m, nil
		}
	case "tab":
		m.feature = m.nextFeature(1)
		m.toast = string(m.feature)
	case "shift+tab":
		m.feature = m.nextFeature(-1)
		m.toast = string(m.feature)
	case "ctrl+1":
		m.feature = FeatureChat
	case "ctrl+2":
		m.feature = FeatureMail
	case "ctrl+3":
		m.feature = FeatureCalendar
	case "ctrl+4":
		m.feature = FeatureMeet
	case "ctrl+5":
		m.feature = FeatureTasks
	case "ctrl+6":
		m.feature = FeatureDrive
	case "ctrl+7":
		m.feature = FeatureDocs
	case "1":
		// Mail's folder rail is the leftmost pane, so 1 focuses it; every
		// other feature has no rail, so 1 keeps focusing the list.
		if m.feature == FeatureMail {
			m.focusMailSidebar()
			return m, nil
		}
		m.focusedPane = paneList
		return m, nil
	case "2":
		if m.feature == FeatureDocs {
			return m.openSelectedDoc()
		}
		if m.feature == FeatureMeet {
			return m, nil
		}
		m.focusedPane = paneDetail
		return m, nil
	case "3":
		if m.feature == FeatureDocs || m.feature == FeatureMeet {
			return m, nil
		}
		m.focusedPane = paneAction
		m.input.Focus()
		m.vimComposer = vimModeInsert
		return m, nil
	case "j", "down":
		if m.focusedPane == paneMailSidebar {
			m.moveMailFolderCursor(1)
			return m, nil
		}
		if m.focusedPane == paneDetail {
			m.detail.LineDown(1)
			return m, nil
		}
		return m.moveSelection(1)
	case "k", "up":
		if m.focusedPane == paneMailSidebar {
			m.moveMailFolderCursor(-1)
			return m, nil
		}
		if m.focusedPane == paneDetail {
			m.detail.LineUp(1)
			return m, nil
		}
		return m.moveSelection(-1)
	case "ctrl+d":
		if m.focusedPane == paneDetail {
			m.detail.HalfViewDown()
			return m, nil
		}
	case "ctrl+u":
		if m.focusedPane == paneDetail {
			m.detail.HalfViewUp()
			return m, nil
		}
	case "ctrl+f", "pgdown":
		if m.focusedPane == paneDetail {
			m.detail.ViewDown()
			return m, nil
		}
	case "ctrl+b", "pgup":
		if m.focusedPane == paneDetail {
			m.detail.ViewUp()
			return m, nil
		}
	case "g":
		if m.focusedPane == paneDetail {
			m.detail.GotoTop()
			return m, nil
		}
		if m.focusedPane == paneMailSidebar {
			m.mailFolderCursor = 0
			return m, nil
		}
		m.selected[m.feature] = 0
		return m.loadSelectedItem()
	case "G":
		if m.focusedPane == paneDetail {
			m.detail.GotoBottom()
			return m, nil
		}
		if m.focusedPane == paneMailSidebar {
			m.mailFolderCursor = max(0, len(m.mailFolderList())-1)
			return m, nil
		}
		m.selected[m.feature] = m.listLen() - 1
		return m.loadSelectedItem()
	case "enter", "o":
		if m.focusedPane == paneMailSidebar {
			return m.selectMailFolder()
		}
		if m.focusedPane == paneDetail {
			if att, ok := m.detailAttachmentAtCursor(); ok {
				if att.IsImage() {
					m.openImageViewer(att)
					return m, nil
				}
				return m.downloadAttachment(att)
			}
			if next, cmd, ok := m.openDetailURLAtCursor(); ok {
				return next, cmd
			}
		}
		if m.feature == FeatureMail && m.focusedPane == paneList {
			// Mail browses the inbox full-screen; opening a thread swaps in
			// the reading pane the same way clicking a Gmail row does.
			m.focusedPane = paneDetail
			return m, nil
		}
		if m.feature == FeatureDocs && m.focusedPane == paneList {
			return m.openSelectedDoc()
		}
		if m.feature == FeatureCalendar && m.focusedPane == paneList {
			if m.selectedEvent().ID == "" {
				m.toast = "no event selected"
				return m, nil
			}
			m.focusedPane = paneDetail
			return m, nil
		}
		if m.feature == FeatureDrive {
			return m.downloadSelectedDriveFile()
		}
		m.toast = m.openHint()
	case "i":
		if m.feature == FeatureDocs {
			return m, nil
		}
		m.focusedPane = paneAction
		m.input.Focus()
		m.vimComposer = vimModeInsert
	case "a":
		if m.feature == FeatureDocs {
			return m, nil
		}
		m.focusedPane = paneAction
		m.input.Focus()
		m.vimComposer = vimModeInsert
		m.input.CursorEnd()
	case "y":
		if m.feature == FeatureCalendar {
			if m.calendarMonthBlocksEventAction() {
				return m, nil
			}
			return m.rsvpSelected("accepted")
		}
		cmd := m.yankFocused()
		return m, cmd
	case "p":
		return m.pasteIntoComposer()
	case "r":
		if m.feature == FeatureChat {
			if msg, ok := m.chatMessageUnderCursor(); ok {
				m.beginThreadReply(msg)
				return m, nil
			}
		}
		if len(m.imageErrors) > 0 {
			m.imageErrors = map[string]string{}
			m.imageVersion++
		}
		return m.refreshCurrentFeature()
	case "ctrl+r":
		cfg, err := LoadConfig()
		if err != nil {
			m.err = err.Error()
			return m, nil
		}
		m.cfg = cfg
		m.theme = theme.New(cfg.Theme, cfg.NoColor)
		m.toast = "config reloaded"
	case "/":
		if m.feature == FeatureChat && m.focusedPane == paneList {
			m.openSpaceFilter()
			return m, nil
		}
		m.openSearchModal()
	case "m":
		return m.loadMore()
	case "s":
		if m.feature == FeatureChat {
			return m, m.toggleChatSubscription()
		} else if m.feature == FeatureMail {
			return m.toggleSelectedStar()
		}
	case "u":
		if m.feature == FeatureMail {
			return m.toggleSelectedUnread()
		}
	case "c":
		if m.feature == FeatureMail {
			m.openMailCompose(nil, mailComposeNew)
		} else if m.feature == FeatureCalendar {
			m.openEventCompose(nil)
		}
	case "R":
		if m.feature == FeatureMail {
			thread := m.selectedMail()
			m.openMailCompose(&thread, mailComposeReply)
		} else if m.feature == FeatureChat {
			m.loading = true
			if len(m.imageErrors) > 0 {
				m.imageErrors = map[string]string{}
				m.imageVersion++
			}
			return m, m.loadAllCmd()
		}
	case "A":
		if m.feature == FeatureMail {
			thread := m.selectedMail()
			m.openMailCompose(&thread, mailComposeReplyAll)
		}
	case "l":
		if m.feature == FeatureMail {
			m.openMailLabelModal()
		}
	case "f":
		if m.feature == FeatureMail {
			thread := m.selectedMail()
			m.openMailCompose(&thread, mailComposeForward)
		}
	case "e":
		if m.feature == FeatureMail {
			return m.archiveSelectedMail()
		}
	case "#":
		if m.feature == FeatureMail {
			return m.trashSelectedMail()
		}
	case "n", "N":
		if m.feature == FeatureChat {
			m.beginCreateChatSpace()
		} else if m.feature == FeatureCalendar {
			if m.calendarMonthBlocksEventAction() {
				return m, nil
			}
			return m.rsvpSelected("declined")
		}
		if m.feature == FeatureMeet {
			return m.createMeetSpaceNow()
		}
	case "M":
		if m.feature == FeatureCalendar {
			if m.calendarMonthBlocksEventAction() {
				return m, nil
			}
			return m.rsvpSelected("tentative")
		}
	case "d":
		if m.feature == FeatureChat {
			return m.deleteSelectedChatMessage()
		}
		if m.feature == FeatureCalendar {
			if m.calendarMonthBlocksEventAction() {
				return m, nil
			}
			return m.deleteSelectedEvent()
		}
		if m.feature == FeatureTasks {
			return m.deleteSelectedTask()
		}
	case " ", "space":
		if m.feature == FeatureTasks {
			return m.toggleSelectedTaskCompleted()
		}
	case "v":
		if m.feature == FeatureCalendar {
			return m.toggleCalendarView()
		}
	case "D":
		if m.feature == FeatureCalendar {
			m.openGoToDateModal()
			return m, nil
		}
	case "t":
		if m.feature == FeatureCalendar {
			// The month grid handles 't' in updateCalendarMonthKey; here the
			// agenda jumps the selection to the first event from today on.
			m.selected[FeatureCalendar] = m.firstEventOnOrAfter(startOfDay(time.Now()))
			m.toast = "today"
			return m, nil
		}
	case "]":
		if m.feature == FeatureCalendar {
			return m.moveCalendar(1)
		} else if m.feature == FeatureTasks {
			return m.moveTaskList(1)
		}
	case "[":
		if m.feature == FeatureCalendar {
			return m.moveCalendar(-1)
		} else if m.feature == FeatureTasks {
			return m.moveTaskList(-1)
		}
	case "J":
		if m.feature == FeatureMeet {
			return m.openMeetLink()
		}
	case "C":
		if m.feature == FeatureMeet {
			return m.copyMeetLink()
		}
	case "E":
		if m.feature == FeatureChat {
			if msg, ok := m.chatMessageUnderCursor(); ok {
				m.beginEditChatMessage(msg)
			} else {
				m.toast = "move cursor onto a message to edit"
			}
			return m, nil
		}
		if m.feature == FeatureCalendar {
			if m.calendarMonthBlocksEventAction() {
				return m, nil
			}
			event := m.selectedEvent()
			if event.ID != "" {
				m.openEventCompose(&event)
			}
			return m, nil
		}
		if m.feature == FeatureMeet {
			return m.endSelectedMeet()
		}
	case ">":
		if m.feature == FeatureCalendar {
			if m.calendarMonthBlocksEventAction() {
				return m, nil
			}
			return m.moveSelectedEventToNextCalendar()
		}
	case "+":
		if m.feature == FeatureChat {
			return m.addSelectedChatReaction()
		}
	case "-":
		if m.feature == FeatureChat {
			return m.removeSelectedChatReaction()
		}
	case "x":
		m.err = ""
		m.toast = ""
	}
	return m, nil
}

func (m Model) submitAction() (Model, tea.Cmd) {
	value := strings.TrimSpace(m.input.Value())
	// Chat allows sending image-only messages (paste + Enter with no text),
	// so the empty-input early return only applies when there's also nothing
	// pending to upload.
	if value == "" && !(m.feature == FeatureChat && len(m.pendingChatAttachments) > 0) {
		m.focusedPane = paneList
		m.input.Blur()
		m.clearReplyContext()
		return m, nil
	}
	switch m.feature {
	case FeatureChat:
		if m.createSpaceMode {
			displayName, members := parseChatSpaceSetupInput(value)
			m.input.SetValue("")
			m.focusedPane = paneList
			m.input.Blur()
			m.clearReplyContext()
			return m, func() tea.Msg {
				var space api.Space
				var err error
				if len(members) > 0 {
					space, err = m.client.SetupChatSpace(m.ctx, displayName, members)
				} else {
					space, err = m.client.CreateChatSpace(m.ctx, displayName)
				}
				return chatSpaceCreatedMsg{space: space, err: err}
			}
		}
		if m.editMessageName != "" {
			messageName := m.editMessageName
			messageID := m.editMessageID
			m.input.SetValue("")
			m.focusedPane = paneList
			m.input.Blur()
			m.clearReplyContext()
			return m, func() tea.Msg {
				msg, err := m.client.EditChatMessage(m.ctx, messageName, value)
				return chatEditedMsg{messageID: messageID, message: msg, err: err}
			}
		}
		space := m.selectedSpace()
		if space.Name == "" {
			return m, nil
		}
		pendingID := fmt.Sprintf("pending-%d", time.Now().UnixNano())
		threadID := m.replyThreadID
		attachments := m.pendingChatAttachments
		m.pendingChatAttachments = nil
		uploads := make([]api.LocalAttachment, 0, len(attachments))
		for _, att := range attachments {
			uploads = append(uploads, api.LocalAttachment{
				Path:        att.path,
				ContentType: att.contentType,
				Name:        att.name,
			})
		}
		pending := api.ChatMessage{
			ID:         pendingID,
			Space:      space.Name,
			SenderID:   "users/me",
			SenderName: "You",
			Text:       value,
			CreateTime: time.Now(),
			Pending:    true,
			ThreadID:   threadID,
		}
		if threadID != "" {
			pending.ParentID = lastSegmentOfName(threadID)
		}
		m.chatMessages, _ = upsertChatMessage(m.chatMessages, pending)
		m.markSeenChatMessage(pending)
		m.input.SetValue("")
		m.focusedPane = paneList
		m.input.Blur()
		m.clearReplyContext()
		return m, func() tea.Msg {
			msg, err := m.client.SendChatMessage(m.ctx, space.Name, value, threadID, uploads)
			if err == nil {
				// Keep the temp files: the returned ChatMessage now points
				// to them via LocalPath so the inline renderer can show the
				// just-sent image without re-downloading from upstream.
				// They get cleaned up by the OS tmp sweep eventually.
				return chatSentMsg{pendingID: pendingID, message: msg}
			}
			// On failure, hand the staged pending attachments back to the
			// handler so the user can retry without re-pasting.
			return chatSentMsg{pendingID: pendingID, message: msg, err: err, attachments: attachments}
		}
	case FeatureCalendar:
		m.input.SetValue("")
		m.focusedPane = paneList
		m.input.Blur()
		return m, func() tea.Msg {
			event, err := m.client.QuickAddEvent(m.ctx, value)
			return eventActionMsg{event: event, err: err, label: "event created"}
		}
	case FeatureMeet:
		m.input.SetValue("")
		m.focusedPane = paneList
		m.input.Blur()
		return m, func() tea.Msg {
			space, err := m.client.CreateMeetSpace(m.ctx, value)
			return meetActionMsg{space: space, err: err, label: "meet space created"}
		}
	}
	return m, nil
}

// loadAllCmd refetches every workspace section in a single command. It backs
// the manual full-reload key; the cold-start path uses startupLoadCmds instead
// so panes can fill in progressively.
func (m Model) loadAllCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 45*time.Second)
		defer cancel()

		auth, authErr := m.client.AuthStatus(ctx)
		spaces, spacesErr := m.client.ChatSpaces(ctx)
		selectedSpace := ""
		if len(spaces.Items) > 0 {
			selectedSpace = spaces.Items[clamp(m.selected[FeatureChat], len(spaces.Items))].Name
		}
		messages, messagesErr := m.client.ChatMessages(ctx, selectedSpace, "")
		labels, labelsErr := m.client.MailLabels(ctx)
		threads, threadsErr := m.client.MailThreads(ctx, api.MailQuery{Label: "Inbox"})
		calendars, calendarsErr := m.client.CalendarLists(ctx)
		calendarID := selectedCalendarID(calendars.Items, m.calendarIndex)
		calendarMonth := monthStart(m.calendarCursorOrToday())
		events, eventsErr := m.client.CalendarEvents(ctx, api.CalendarQuery{CalendarID: calendarID, TimeMin: calendarMonth, TimeMax: calendarMonth.AddDate(0, 1, 0)})
		meet, meetErr := m.client.MeetSpaces(ctx)
		taskLists, taskListsErr := m.client.TaskLists(ctx)
		tasks := api.Page[api.TaskItem]{}
		taskListID := ""
		var tasksErr error
		if len(taskLists.Items) > 0 {
			taskListID = taskLists.Items[clamp(m.taskListIndex, len(taskLists.Items))].ID
			tasks, tasksErr = m.client.Tasks(ctx, api.TaskQuery{TaskListID: taskListID})
		}
		driveFiles, driveErr := m.client.DriveFiles(ctx, api.DriveQuery{})
		docFiles, docsErr := m.client.Docs(ctx, api.DriveQuery{})
		doc := api.DocDocument{}
		var docErr error
		if len(docFiles.Items) > 0 {
			doc = api.DocDocument{ID: docFiles.Items[clamp(m.selected[FeatureDocs], len(docFiles.Items))].ID}
			doc, docErr = m.client.Doc(ctx, doc.ID)
		}

		err := firstErr(authErr, spacesErr, messagesErr, labelsErr, threadsErr, calendarsErr, eventsErr, meetErr, taskListsErr, tasksErr, driveErr, docsErr, docErr)
		return loadedMsg{
			auth: auth, spaces: spaces, messages: messages, labels: labels, threads: threads, events: events, calendars: calendars, calendarID: calendarID, calendarMonth: calendarMonth, meet: meet,
			taskLists: taskLists, tasks: tasks, taskListID: taskListID, driveFiles: driveFiles, docFiles: docFiles, doc: doc,
			err: err, authRequired: !auth.Valid(),
		}
	}
}

// startupLoadCmds fans the cold-start fetch out into one command per pane.
// tea.Batch runs each in its own goroutine, so the requests hit the daemon in
// parallel and every pane renders the moment its own response lands. Drive and
// Docs are intentionally absent — they load lazily on first visit.
func (m Model) startupLoadCmds() []tea.Cmd {
	return []tea.Cmd{
		m.loadAuthCmd(),
		m.loadChatSectionCmd(),
		m.loadMailSectionCmd(),
		m.loadCalendarSectionCmd(),
		m.loadMeetSectionCmd(),
		m.loadTasksSectionCmd(),
	}
}

// ensureFeatureLoadedCmd returns the fetch command for a lazily-loaded pane
// (Drive, Docs) the first time it is opened, or nil if it is already loaded,
// already in flight, or eagerly loaded at startup.
func (m *Model) ensureFeatureLoadedCmd(f Feature) tea.Cmd {
	if m.featureLoaded[f] || m.featureLoading[f] {
		return nil
	}
	switch f {
	case FeatureDrive:
		m.featureLoading[f] = true
		return m.loadDriveSectionCmd()
	case FeatureDocs:
		m.featureLoading[f] = true
		return m.loadDocsSectionCmd()
	}
	return nil
}

func (m Model) loadAuthCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
		defer cancel()
		auth, err := m.client.AuthStatus(ctx)
		return authLoadedMsg{auth: auth, err: err}
	}
}

func (m Model) refreshCurrentFeature() (Model, tea.Cmd) {
	switch m.feature {
	case FeatureChat:
		return m.refreshSelectedChat()
	case FeatureMail:
		m.loading = true
		search := m.search
		folder := m.mailFolderByName(m.mailFolder)
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(m.ctx, 12*time.Second)
			defer cancel()

			labels, labelsErr := m.client.MailLabels(ctx)
			threads, threadsErr := m.client.MailThreads(ctx, mailQueryForFolder(folder, search, ""))
			return featureRefreshedMsg{
				feature: FeatureMail,
				labels:  labels,
				threads: threads,
				err:     firstErr(labelsErr, threadsErr),
			}
		}
	case FeatureCalendar:
		if m.calendarView == calViewMonth {
			return m.loadCalendarMonth(m.calendarCursorOrToday())
		}
		m.loading = true
		search := m.search
		calendarID := m.selectedCalendar().ID
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(m.ctx, 12*time.Second)
			defer cancel()

			calendars, calendarsErr := m.client.CalendarLists(ctx)
			if calendarID == "" {
				calendarID = selectedCalendarID(calendars.Items, m.calendarIndex)
			}
			events, eventsErr := m.client.CalendarEvents(ctx, api.CalendarQuery{CalendarID: calendarID, Search: search})
			return featureRefreshedMsg{feature: FeatureCalendar, calendars: calendars, calendarID: calendarID, events: events, err: firstErr(calendarsErr, eventsErr)}
		}
	case FeatureMeet:
		m.loading = true
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(m.ctx, 12*time.Second)
			defer cancel()

			meet, err := m.client.MeetSpaces(ctx)
			return featureRefreshedMsg{feature: FeatureMeet, meet: meet, err: err}
		}
	case FeatureTasks:
		m.loading = true
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(m.ctx, 12*time.Second)
			defer cancel()

			taskLists, taskListsErr := m.client.TaskLists(ctx)
			tasks := api.Page[api.TaskItem]{}
			taskListID := ""
			var tasksErr error
			if len(taskLists.Items) > 0 {
				taskListID = taskLists.Items[clamp(m.taskListIndex, len(taskLists.Items))].ID
				tasks, tasksErr = m.client.Tasks(ctx, api.TaskQuery{TaskListID: taskListID})
			}
			return featureRefreshedMsg{feature: FeatureTasks, taskLists: taskLists, tasks: tasks, taskListID: taskListID, err: firstErr(taskListsErr, tasksErr)}
		}
	case FeatureDrive:
		m.loading = true
		search := m.search
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(m.ctx, 12*time.Second)
			defer cancel()

			files, err := m.client.DriveFiles(ctx, api.DriveQuery{Search: search})
			return featureRefreshedMsg{feature: FeatureDrive, driveFiles: files, err: err}
		}
	case FeatureDocs:
		m.loading = true
		search := m.search
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(m.ctx, 12*time.Second)
			defer cancel()

			files, filesErr := m.client.Docs(ctx, api.DriveQuery{Search: search})
			doc := api.DocDocument{}
			var docErr error
			if len(files.Items) > 0 {
				doc, docErr = m.client.Doc(ctx, files.Items[clamp(m.selected[FeatureDocs], len(files.Items))].ID)
			}
			return featureRefreshedMsg{feature: FeatureDocs, docFiles: files, doc: doc, err: firstErr(filesErr, docErr)}
		}
	default:
		return m, nil
	}
}

func (m Model) loadMore() (Model, tea.Cmd) {
	switch m.feature {
	case FeatureChat:
		if m.chatOlder == "" {
			m.toast = "no older messages"
			return m, nil
		}
		space := m.selectedSpace()
		token := m.chatOlder
		m.loading = true
		return m, func() tea.Msg {
			page, err := m.client.ChatMessages(m.ctx, space.Name, token)
			return featureLoadedMsg{feature: FeatureChat, items: page.Items, next: page.NextPageToken, err: err}
		}
	case FeatureMail:
		if m.mailNext == "" {
			m.toast = "no more mail"
			return m, nil
		}
		token := m.mailNext
		search := m.search
		folder := m.mailFolderByName(m.mailFolder)
		m.loading = true
		return m, func() tea.Msg {
			page, err := m.client.MailThreads(m.ctx, mailQueryForFolder(folder, search, token))
			return featureLoadedMsg{feature: FeatureMail, items: page.Items, next: page.NextPageToken, err: err}
		}
	case FeatureCalendar:
		if m.calendarView == calViewMonth {
			m.toast = "month view already shows the whole month"
			return m, nil
		}
		if m.calendarNext == "" {
			m.toast = "no more events"
			return m, nil
		}
		token := m.calendarNext
		calendarID := m.selectedCalendar().ID
		m.loading = true
		return m, func() tea.Msg {
			page, err := m.client.CalendarEvents(m.ctx, api.CalendarQuery{CalendarID: calendarID, Search: m.search, PageToken: token})
			return featureLoadedMsg{feature: FeatureCalendar, items: page.Items, next: page.NextPageToken, err: err}
		}
	case FeatureTasks:
		if m.taskNext == "" {
			m.toast = "no more tasks"
			return m, nil
		}
		list := m.selectedTaskList()
		token := m.taskNext
		m.loading = true
		return m, func() tea.Msg {
			page, err := m.client.Tasks(m.ctx, api.TaskQuery{TaskListID: list.ID, PageToken: token})
			return featureLoadedMsg{feature: FeatureTasks, items: page.Items, next: page.NextPageToken, err: err}
		}
	case FeatureDrive:
		if m.driveNext == "" {
			m.toast = "no more drive files"
			return m, nil
		}
		token := m.driveNext
		m.loading = true
		return m, func() tea.Msg {
			page, err := m.client.DriveFiles(m.ctx, api.DriveQuery{Search: m.search, PageToken: token})
			return featureLoadedMsg{feature: FeatureDrive, items: page.Items, next: page.NextPageToken, err: err}
		}
	case FeatureDocs:
		if m.docNext == "" {
			m.toast = "no more docs"
			return m, nil
		}
		token := m.docNext
		m.loading = true
		return m, func() tea.Msg {
			page, err := m.client.Docs(m.ctx, api.DriveQuery{Search: m.search, PageToken: token})
			return featureLoadedMsg{feature: FeatureDocs, items: page.Items, next: page.NextPageToken, err: err}
		}
	}
	return m, nil
}

func (m Model) autosaveTick() tea.Cmd {
	return tea.Tick(5*time.Second, func(time.Time) tea.Msg { return autosaveMsg{} })
}

func (m *Model) resize() {
	w, h := m.width, m.height
	if w <= 0 {
		w = 100
	}
	if h <= 0 {
		h = 32
	}
	leftHBorder := m.theme.Pane.GetHorizontalBorderSize()
	detailHBorder := m.theme.Active.GetHorizontalBorderSize()
	detailVBorder := m.theme.Active.GetVerticalBorderSize()
	actionVBorder := m.theme.Input.GetVerticalBorderSize()
	detailHPad := m.theme.Active.GetHorizontalPadding()
	actionHPad := m.theme.Input.GetHorizontalPadding()
	statusH := 1

	// Mail's Gmail-style layout puts the sidebar on the left, so the reading
	// pane and composer are sized against the wider right column instead of
	// the shared 30/70 split below.
	if m.feature == FeatureMail {
		sidebarW := mailSidebarWidth(w)
		mainW := max(30, w-sidebarW-leftHBorder-detailHBorder)
		actionHeight := max(3, min(8, strings.Count(m.input.Value(), "\n")+3))
		// The composer pane only takes height while it is focused; otherwise
		// the reading pane fills the whole right column.
		mainContentH := max(5, h-statusH-detailVBorder)
		if m.focusedPane == paneAction {
			mainContentH = max(5, h-statusH-detailVBorder-actionHeight-actionVBorder)
		}
		mailDetailWidth := max(10, mainW-detailHPad)
		if m.cfg.VimMode {
			mailDetailWidth = max(10, mailDetailWidth-2)
		}
		m.detail.Width = mailDetailWidth
		m.detail.Height = max(3, mainContentH)
		m.input.SetWidth(max(10, mainW-actionHPad))
		m.input.SetHeight(max(2, actionHeight))
		return
	}

	if m.feature == FeatureDocs {
		mainW := max(20, w-detailHBorder)
		mainH := max(5, h-statusH-detailVBorder)
		detailWidth := max(10, mainW-detailHPad)
		if m.cfg.VimMode {
			detailWidth = max(10, detailWidth-2)
		}
		m.detail.Width = detailWidth
		m.detail.Height = max(3, mainH)
		m.input.SetWidth(max(10, mainW-actionHPad))
		m.input.SetHeight(2)
		return
	}

	left := max(20, int(float64(w)*0.30)-leftHBorder)
	right := max(20, w-left-leftHBorder-detailHBorder)

	actionHeight := max(3, min(8, strings.Count(m.input.Value(), "\n")+3))
	detailHeight := max(5, h-statusH-detailVBorder-actionHeight-actionVBorder)

	detailWidth := max(10, right-detailHPad)
	if m.cfg.VimMode {
		detailWidth = max(10, detailWidth-2)
	}
	m.detail.Width = detailWidth
	m.detail.Height = max(3, detailHeight)
	m.input.SetWidth(max(10, right-actionHPad))
	m.input.SetHeight(max(2, actionHeight))
}

func (m *Model) clampSelections() {
	for _, feature := range featureOrder {
		m.selected[feature] = clamp(m.selected[feature], m.listLenFor(feature))
	}
	m.taskListIndex = clamp(m.taskListIndex, len(m.taskLists))
	m.clampCalendarDayEventCursor()
}

func (m Model) nextFeature(delta int) Feature {
	idx := 0
	for i, feature := range featureOrder {
		if feature == m.feature {
			idx = i
			break
		}
	}
	idx = (idx + delta + len(featureOrder)) % len(featureOrder)
	return featureOrder[idx]
}

func (m Model) moveSelection(delta int) (Model, tea.Cmd) {
	// The month grid has no list index — wheel/arrow steps move by a week.
	if m.feature == FeatureCalendar && m.calendarView == calViewMonth {
		return m.calendarShiftCursor(7*delta, 0)
	}
	length := m.listLen()
	if length == 0 {
		m.selected[m.feature] = 0
		return m, nil
	}
	next := clamp(m.selected[m.feature]+delta, length)
	if next == m.selected[m.feature] {
		return m, nil
	}
	m.selected[m.feature] = next
	return m.loadSelectedItem()
}

func (m Model) loadSelectedItem() (Model, tea.Cmd) {
	switch m.feature {
	case FeatureChat:
		return m.loadSelectedChat()
	case FeatureDocs:
		return m.loadSelectedDoc()
	default:
		return m, nil
	}
}

func (m Model) listLen() int {
	return m.listLenFor(m.feature)
}

func (m Model) listLenFor(feature Feature) int {
	switch feature {
	case FeatureChat:
		return len(m.visibleSpaces())
	case FeatureMail:
		return len(m.mailThreads)
	case FeatureCalendar:
		return len(m.events)
	case FeatureMeet:
		return len(m.meetSpaces)
	case FeatureTasks:
		return len(m.tasks)
	case FeatureDrive:
		return len(m.driveFiles)
	case FeatureDocs:
		return len(m.docFiles)
	default:
		return 0
	}
}

func (m *Model) yankFocused() tea.Cmd {
	var text string
	switch m.feature {
	case FeatureChat:
		if len(m.chatMessages) > 0 {
			text = m.chatMessages[len(m.chatMessages)-1].Text
		}
	case FeatureMail:
		thread := m.selectedMail()
		text = thread.Subject
		if thread.Body != "" {
			text = thread.Subject + "\n\n" + thread.Body
		}
	case FeatureCalendar:
		event := m.selectedEvent()
		text = event.Summary
	case FeatureMeet:
		text = m.selectedMeet().JoinURL()
	case FeatureTasks:
		task := m.selectedTask()
		text = task.Title
		if task.Notes != "" {
			text = task.Title + "\n\n" + task.Notes
		}
	case FeatureDrive:
		file := m.selectedDriveFile()
		text = file.Name
		if file.WebViewLink != "" {
			text = file.Name + "\n" + file.WebViewLink
		}
	case FeatureDocs:
		text = m.doc.Body
		if text == "" {
			text = m.selectedDocFile().WebViewLink
		}
	}
	if text == "" {
		m.toast = "nothing to yank"
		return nil
	}
	m.vimRegister = text
	m.vimRegisterLine = false
	if err := copyText(text); err != nil {
		m.toast = "yank: " + err.Error()
	} else {
		m.toast = "yanked to clipboard"
	}
	return nil
}

func (m Model) pasteIntoComposer() (Model, tea.Cmd) {
	text, err := pasteText()
	if err != nil || text == "" {
		m.toast = "clipboard empty"
		return m, nil
	}
	m.focusedPane = paneAction
	m.input.Focus()
	m.vimComposer = vimModeInsert
	if current := m.input.Value(); current != "" {
		m.input.SetValue(current + text)
	} else {
		m.input.SetValue(text)
	}
	m.input.CursorEnd()
	return m, nil
}

func (m Model) openHint() string {
	switch m.feature {
	case FeatureChat:
		return "space opened"
	case FeatureMail:
		return "thread opened"
	case FeatureCalendar:
		return "event opened"
	case FeatureMeet:
		return "meet details opened"
	case FeatureTasks:
		return "task opened"
	case FeatureDrive:
		return "file opened"
	case FeatureDocs:
		return "document opened"
	default:
		return ""
	}
}

func (m *Model) persist() {
	state := persistedState{
		LastFeature: string(m.feature),
		LastSpace:   m.selectedSpace().Name,
		Selections:  map[string]int{},
	}
	for feature, index := range m.selected {
		state.Selections[string(feature)] = index
	}
	_ = savePersistedState(m.cfg.StatePath, state)
}

func (m *Model) updateDetailContent() {
	if m.width == 0 {
		return
	}
	wasAtBottom := m.detail.AtBottom()
	prevLast := m.detailLineCount - 1
	wasAtLastLine := prevLast >= 0 && m.detailCursor >= prevLast

	selectionKey := m.detailKeyForSelection()
	keyChanged := selectionKey != m.detailKey
	if keyChanged {
		m.detailResetCursor()
		m.detailKey = selectionKey
		wasAtLastLine = false
	}

	renderKey := m.detailRenderFingerprint()
	if renderKey == m.detailRenderKey {
		return
	}

	// Chat history reads bottom-up (oldest → newest), so whenever a chat is
	// first opened or the user switches to a different space the latest
	// messages should already be in view without manual scrolling.
	pinToBottom := m.feature == FeatureChat && keyChanged

	if m.detailImageAt == nil {
		m.detailImageAt = map[int]api.Attachment{}
	} else {
		for k := range m.detailImageAt {
			delete(m.detailImageAt, k)
		}
	}
	if m.detailAttachmentAt == nil {
		m.detailAttachmentAt = map[int]api.Attachment{}
	} else {
		for k := range m.detailAttachmentAt {
			delete(m.detailAttachmentAt, k)
		}
	}
	if m.detailMessageAt == nil {
		m.detailMessageAt = map[int]string{}
	} else {
		for k := range m.detailMessageAt {
			delete(m.detailMessageAt, k)
		}
	}
	decorated, plain := m.decorateDetail(m.detailContent())
	m.detailLines = plain
	m.detailLineCount = len(plain)
	m.detailClampCursor()

	m.detail.SetContent(decorated)
	m.detailRenderKey = renderKey
	if wasAtBottom || pinToBottom {
		m.detail.GotoBottom()
	}
	if m.detailVimEnabled() {
		if (wasAtLastLine || pinToBottom) && m.detailLineCount > 0 {
			m.detailCursor = m.detailLineCount - 1
		}
		m.detailEnsureCursorVisible()
	}
}

func (m Model) detailRenderFingerprint() string {
	var b strings.Builder
	fmt.Fprintf(&b, "feature=%s|selection=%s|width=%d|height=%d|focus=%d|vim=%t|color=%t|icons=%t|inline=%t|image=%d|placement=%d",
		m.feature,
		m.detailKeyForSelection(),
		m.detail.Width,
		m.detail.Height,
		m.focusedPane,
		m.cfg.VimMode,
		!m.cfg.NoColor,
		!m.cfg.NoIcons,
		m.cfg.InlineImages,
		m.imageVersion,
		m.imagePlacement,
	)
	if m.detailVimEnabled() {
		fmt.Fprintf(&b, "|cursor=%d:%d|anchor=%d:%d|visual=%t|visualLine=%t",
			m.detailCursor,
			m.detailCol,
			m.detailAnchor,
			m.detailAnchorCol,
			m.detailVisual,
			m.detailVisualLine,
		)
	}

	switch m.feature {
	case FeatureChat:
		space := m.selectedSpace()
		fmt.Fprintf(&b, "|chatLoading=%t|chatLoadSpace=%s|spaceFilter=%t,%s|space=%s|messages=%d",
			m.chatLoading,
			m.chatLoadSpace,
			m.spaceFilterActive,
			m.spaceFilter,
			space.Name,
			len(m.chatMessages),
		)
		for _, msg := range m.chatMessages {
			label := m.senderLabel(msg)
			fmt.Fprintf(&b, "|msg=%s,%s,%s,%t,%t,%d,%s,%s",
				msg.ID,
				msg.ParentID,
				msg.SenderID,
				m.isSelfMessage(msg, label),
				msg.Pending,
				msg.CreateTime.UnixNano(),
				label,
				msg.Text,
			)
			writeAttachmentFingerprint(&b, msg.Attachments)
		}
	case FeatureMail:
		thread := m.selectedMail()
		fmt.Fprintf(&b, "|mail=%s,%s,%s,%s,%d,%t,%t,%d",
			thread.ID,
			thread.Sender,
			thread.Subject,
			thread.Body,
			thread.Date.UnixNano(),
			thread.Unread,
			thread.Starred,
			thread.QuotedLines,
		)
		writeAttachmentFingerprint(&b, thread.Attachments)
	case FeatureCalendar:
		if m.calendarView == calViewMonth && m.focusedPane != paneDetail {
			day := m.calendarCursorOrToday()
			dayEvents := eventsOnDay(m.monthEvents, day)
			fmt.Fprintf(&b, "|calmonth=%s,%t,%d,%d", day.Format("2006-01-02"), m.loading, len(dayEvents), m.calendarDayEventCursor)
			for _, e := range dayEvents {
				fmt.Fprintf(&b, ",%s:%s:%d:%s", e.ID, e.Summary, e.Start.UnixNano(), e.RSVP)
			}
			break
		}
		event := m.selectedEvent()
		fmt.Fprintf(&b, "|event=%s,%s,%s,%d,%d,%s,%s,%s,%s,%t,%t",
			event.ID,
			event.Summary,
			event.Description,
			event.Start.UnixNano(),
			event.End.UnixNano(),
			event.Location,
			event.HangoutLink,
			event.RSVP,
			strings.Join(event.Attendees, ","),
			event.AllDay,
			event.Recurring,
		)
		if m.calendarFeedback.eventID == event.ID {
			fmt.Fprintf(&b, "|calendarFeedback=%s", m.calendarFeedback.response)
		}
	case FeatureMeet:
		space := m.selectedMeet()
		activeConference := ""
		if space.ActiveConference != nil {
			activeConference = space.ActiveConference.ConferenceRecord
		}
		fmt.Fprintf(&b, "|meet=%s,%s,%s,%s,%d,%s,%d,%t,%t,%s",
			space.Name,
			space.SpaceName,
			space.MeetingURI,
			space.MeetingCode,
			space.Created.UnixNano(),
			space.AccessType(),
			space.ActiveParticipants,
			space.Recording,
			space.Active,
			activeConference,
		)
	case FeatureTasks:
		list := m.selectedTaskList()
		task := m.selectedTask()
		fmt.Fprintf(&b, "|taskList=%s,%s|task=%s,%s,%s,%s,%d,%d,%d",
			list.ID,
			list.Title,
			task.ID,
			task.Title,
			task.Notes,
			task.Status,
			task.Due.UnixNano(),
			task.Completed.UnixNano(),
			task.Updated.UnixNano(),
		)
	case FeatureDrive:
		file := m.selectedDriveFile()
		fmt.Fprintf(&b, "|drive=%s,%s,%s,%d,%s,%d",
			file.ID,
			file.Name,
			file.MimeType,
			file.ModifiedTime.UnixNano(),
			file.WebViewLink,
			file.Size,
		)
	case FeatureDocs:
		file := m.selectedDocFile()
		fmt.Fprintf(&b, "|docFile=%s,%s,%d|doc=%s,%s,%s,%t",
			file.ID,
			file.Name,
			file.ModifiedTime.UnixNano(),
			m.doc.ID,
			m.doc.Title,
			m.doc.Body,
			m.docLoadingID == file.ID,
		)
		writeDocFingerprint(&b, m.doc)
	}
	return b.String()
}

func writeDocFingerprint(b *strings.Builder, doc api.DocDocument) {
	writeAttachmentFingerprint(b, doc.Attachments)
	for _, block := range doc.Blocks {
		fmt.Fprintf(b, "|block=%s,%d,%d,%s", block.Kind, block.Level, block.ListLevel, block.Text)
		for _, inline := range block.Inlines {
			fmt.Fprintf(b, ",inline=%s,%t,%t,%t,%t,%s",
				inline.Text,
				inline.Bold,
				inline.Italic,
				inline.Underline,
				inline.Strikethrough,
				inline.LinkURL,
			)
		}
		for _, row := range block.Rows {
			fmt.Fprintf(b, ",row=%s", strings.Join(row, "\x1f"))
		}
		if block.Attachment != nil {
			fmt.Fprintf(b, ",image=%s", block.Attachment.PreviewSource())
		}
	}
}

func writeAttachmentFingerprint(b *strings.Builder, attachments []api.Attachment) {
	normalized := api.NormalizeAttachments(attachments)
	fmt.Fprintf(b, "|attachments=%d", len(normalized))
	for _, attachment := range normalized {
		fmt.Fprintf(b, ",%s,%s,%s,%s,%s,%s,%s",
			attachment.ID,
			attachment.MediaResourceName(),
			attachment.PreviewSource(),
			attachment.DisplayName(),
			attachment.ContentType,
			attachment.DownloadURL,
			attachment.ThumbnailURL,
		)
	}
}

func (m Model) detailKeyForSelection() string {
	switch m.feature {
	case FeatureChat:
		return "chat:" + m.selectedSpace().Name
	case FeatureMail:
		return "mail:" + m.selectedMail().ID
	case FeatureCalendar:
		if m.calendarView == calViewMonth && m.focusedPane != paneDetail {
			return fmt.Sprintf("calmonth:%s:%d", m.calendarCursorOrToday().Format("2006-01-02"), m.calendarDayEventCursor)
		}
		return "cal:" + m.selectedEvent().ID
	case FeatureMeet:
		return "meet:" + m.selectedMeet().Name
	case FeatureTasks:
		return "tasks:" + m.selectedTaskList().ID + ":" + m.selectedTask().ID
	case FeatureDrive:
		return "drive:" + m.selectedDriveFile().ID
	case FeatureDocs:
		return "docs:" + m.selectedDocFile().ID
	default:
		return string(m.feature)
	}
}

func clamp(value, length int) int {
	if length <= 0 {
		return 0
	}
	if value < 0 {
		return 0
	}
	if value >= length {
		return length - 1
	}
	return value
}

func firstErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func splitCSV(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func (m Model) saveDraft() error {
	if m.modal == nil {
		return nil
	}
	if m.cfg.Daemon {
		saver, ok := m.client.(interface {
			DraftSave(context.Context, string, map[string]any) error
		})
		if !ok {
			return nil
		}
		ctx, cancel := context.WithTimeout(m.ctx, 3*time.Second)
		defer cancel()
		return saver.DraftSave(ctx, m.modal.id, m.modal.snapshot())
	}
	if err := os.MkdirAll(m.cfg.DraftDir, 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(m.modal.snapshot(), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(m.cfg.DraftDir, m.modal.id+".json"), append(payload, '\n'), 0o600)
}
