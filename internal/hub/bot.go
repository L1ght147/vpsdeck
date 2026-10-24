package hub

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	tg "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/L1ght147/vpsdeck/internal/i18n"
)

// Callback data. Every screen is one message that buttons edit in place;
// /start is the only command.
const (
	cbMenu     = "menu"
	cbList     = "list"
	cbProblems = "prob"
	cbUpdates  = "upd"
	cbAdd      = "add"
	cbAddAuto  = "add:auto"
	cbSettings = "set"
	cbLang     = "lang"
)

type Bot struct {
	hub *Hub
	api *tg.Bot
	// ctx is the hub's run context, used by Notify which has no context of its own.
	ctx context.Context

	mu sync.Mutex
	// awaitingName maps a chat to the prompt message waiting for a new server's name.
	awaitingName map[int64]int
}

func NewBot(h *Hub) (*Bot, error) {
	b := &Bot{hub: h, ctx: context.Background(), awaitingName: map[int64]int{}}
	api, err := tg.New(h.cfg.BotToken,
		tg.WithDefaultHandler(b.onText),
		tg.WithMiddlewares(b.adminOnly),
		tg.WithErrorsHandler(func(err error) { slog.Warn("telegram", "err", err) }),
	)
	if err != nil {
		return nil, err
	}
	b.api = api

	api.RegisterHandler(tg.HandlerTypeMessageText, "start", tg.MatchTypeCommand, b.onStart)
	for data, fn := range map[string]tg.HandlerFunc{
		cbMenu: b.onMenu, cbList: b.onList, cbProblems: b.onProblems, cbUpdates: b.onUpdates,
		cbAdd: b.onAdd, cbAddAuto: b.onAddAuto, cbSettings: b.onSettings, cbLang: b.onLang,
	} {
		api.RegisterHandler(tg.HandlerTypeCallbackQueryData, data, tg.MatchTypeExact, fn)
	}
	for prefix, fn := range map[string]tg.HandlerFunc{
		"srv:": b.onCard, "pkg:": b.onPackages, "mute:": b.onMute, "del:": b.onDelete, "delok:": b.onDeleteConfirm,
	} {
		api.RegisterHandler(tg.HandlerTypeCallbackQueryData, prefix, tg.MatchTypePrefix, fn)
	}
	return b, nil
}

func (b *Bot) Start(ctx context.Context) {
	b.setCommands(ctx)
	n := 0
	if servers, err := b.hub.store.Servers(); err == nil {
		n = len(servers)
	}
	b.Broadcast(ctx, i18n.T(b.hub.Lang(), "bot.hub_started", html.EscapeString(b.hub.version), n))
	b.api.Start(ctx)
}

// setCommands leaves /start as the only command, in both languages.
func (b *Bot) setCommands(ctx context.Context) {
	for _, lang := range i18n.Langs {
		params := &tg.SetMyCommandsParams{Commands: []models.BotCommand{{Command: "start", Description: i18n.T(lang, "cmd.start")}}}
		if lang != "en" {
			params.LanguageCode = lang
		}
		if _, err := b.api.SetMyCommands(ctx, params); err != nil {
			slog.Warn("set commands", "lang", lang, "err", err)
		}
	}
}

func (b *Bot) adminOnly(next tg.HandlerFunc) tg.HandlerFunc {
	return func(ctx context.Context, api *tg.Bot, u *models.Update) {
		var from int64
		switch {
		case u.Message != nil && u.Message.From != nil:
			from = u.Message.From.ID
		case u.CallbackQuery != nil:
			from = u.CallbackQuery.From.ID
		}
		if from == 0 || !b.hub.cfg.IsAdmin(from) {
			return
		}
		next(ctx, api, u)
	}
}

// ---------------------------------------------------------------- sending

type keyboard = [][]models.InlineKeyboardButton

func btn(text, data string) models.InlineKeyboardButton {
	return models.InlineKeyboardButton{Text: text, CallbackData: data}
}

func markup(rows keyboard) models.ReplyMarkup {
	if len(rows) == 0 {
		return nil
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func (b *Bot) send(ctx context.Context, chatID int64, text string, rows keyboard) int {
	m, err := b.api.SendMessage(ctx, &tg.SendMessageParams{
		ChatID:             chatID,
		Text:               text,
		ParseMode:          models.ParseModeHTML,
		ReplyMarkup:        markup(rows),
		LinkPreviewOptions: &models.LinkPreviewOptions{IsDisabled: tg.True()},
	})
	if err != nil {
		slog.Warn("send message", "chat", chatID, "err", err)
		return 0
	}
	return m.ID
}

func (b *Bot) editMessage(ctx context.Context, chatID int64, msgID int, text string, rows keyboard) {
	_, err := b.api.EditMessageText(ctx, &tg.EditMessageTextParams{
		ChatID:             chatID,
		MessageID:          msgID,
		Text:               text,
		ParseMode:          models.ParseModeHTML,
		ReplyMarkup:        markup(rows),
		LinkPreviewOptions: &models.LinkPreviewOptions{IsDisabled: tg.True()},
	})
	// Refreshing an unchanged screen is not an error worth logging.
	if err != nil && !strings.Contains(err.Error(), "message is not modified") {
		slog.Warn("edit message", "err", err)
	}
}

// show replaces the screen a button belongs to.
func (b *Bot) show(ctx context.Context, u *models.Update, text string, rows keyboard) {
	_, _ = b.api.AnswerCallbackQuery(ctx, &tg.AnswerCallbackQueryParams{CallbackQueryID: u.CallbackQuery.ID})
	if m := u.CallbackQuery.Message.Message; m != nil {
		b.editMessage(ctx, m.Chat.ID, m.ID, text, rows)
	}
}

// toast answers a button press with a short popup.
func (b *Bot) toast(ctx context.Context, u *models.Update, text string) {
	_, _ = b.api.AnswerCallbackQuery(ctx, &tg.AnswerCallbackQueryParams{CallbackQueryID: u.CallbackQuery.ID, Text: text})
}

func callbackChat(u *models.Update) int64 {
	if m := u.CallbackQuery.Message.Message; m != nil {
		return m.Chat.ID
	}
	return u.CallbackQuery.From.ID
}

func (b *Bot) Broadcast(ctx context.Context, text string) {
	for _, id := range b.hub.cfg.Admins {
		b.send(ctx, id, text, nil)
	}
}

// Notify sends an alert to every admin with a link to the server card.
func (b *Bot) Notify(e Event) {
	lang := b.hub.Lang()
	// ":n" opens the card as a new message, so the alert itself stays in the chat.
	rows := keyboard{{btn(i18n.T(lang, "btn.details"), fmt.Sprintf("srv:%d:n", e.ServerID))}}
	if e.Level != LevelOK && e.Kind != KindOnline {
		rows = append(rows,
			[]models.InlineKeyboardButton{btn(i18n.T(lang, "btn.mute_1h"), fmt.Sprintf("mute:%d:1", e.ServerID))},
			[]models.InlineKeyboardButton{btn(i18n.T(lang, "btn.mute_24h"), fmt.Sprintf("mute:%d:24", e.ServerID))},
		)
	}
	text := FormatEvent(lang, e)
	for _, id := range b.hub.cfg.Admins {
		b.send(b.ctx, id, text, rows)
	}
}

// ---------------------------------------------------------------- screens

func menuRow(lang string) []models.InlineKeyboardButton {
	return []models.InlineKeyboardButton{btn(i18n.T(lang, "btn.menu"), cbMenu)}
}

// menuScreen is laid out from the main to the auxiliary: viewing, then the
// one action, then service buttons. Counts on the buttons show where to look.
func (b *Bot) menuScreen(lang string) (string, keyboard) {
	text, st, err := b.hub.MainMenu(lang)
	if err != nil {
		slog.Error("main menu", "err", err)
		text = i18n.T(lang, "bot.error")
	}
	return text, keyboard{
		{btn(i18n.T(lang, "btn.servers_n", st.Total), cbList)},
		{btn(i18n.T(lang, "btn.problems_n", st.Problems), cbProblems), btn(i18n.T(lang, "btn.updates_n", st.Updates), cbUpdates)},
		{btn(i18n.T(lang, "btn.add"), cbAdd), btn(i18n.T(lang, "btn.settings"), cbSettings)},
	}
}

func (b *Bot) problemsScreen(lang string) (string, keyboard) {
	text, rows, err := b.hub.Problems(lang)
	if err != nil {
		slog.Error("problems", "err", err)
		return i18n.T(lang, "bot.error"), keyboard{menuRow(lang)}
	}
	kb := make(keyboard, 0, len(rows)+1)
	for _, r := range rows {
		kb = append(kb, []models.InlineKeyboardButton{btn(r.Label, fmt.Sprintf("srv:%d:%s", r.ID, originProblems))})
	}
	kb = append(kb, menuRow(lang))
	return text, kb
}

func (b *Bot) listScreen(lang string) (string, keyboard) {
	servers, err := b.hub.store.Servers()
	if err != nil {
		slog.Error("list servers", "err", err)
		return i18n.T(lang, "bot.error"), keyboard{menuRow(lang)}
	}
	if len(servers) == 0 {
		return i18n.T(lang, "list.empty"), keyboard{{btn(i18n.T(lang, "btn.add"), cbAdd)}, menuRow(lang)}
	}
	now := time.Now()
	rows := make(keyboard, 0, len(servers)+1)
	for _, s := range servers {
		label := i18n.T(lang, "list.row", s.Name, statusText(lang, b.hub.status(s, now)))
		if r := s.Report; r != nil {
			label += i18n.T(lang, "list.disk", r.MaxDiskPercent())
			if r.Updates.Security > 0 {
				label += i18n.T(lang, "list.security", r.Updates.Security)
			}
		}
		rows = append(rows, []models.InlineKeyboardButton{btn(label, fmt.Sprintf("srv:%d", s.ID))})
	}
	rows = append(rows, menuRow(lang))
	return i18n.T(lang, "list.header", len(servers)), rows
}

func (b *Bot) updatesScreen(lang string) (string, keyboard) {
	servers, err := b.hub.store.Servers()
	if err != nil {
		slog.Error("list servers", "err", err)
		return i18n.T(lang, "bot.error"), keyboard{menuRow(lang)}
	}
	var rows keyboard
	for _, s := range servers {
		if !hasUpdates(s) {
			continue
		}
		u := s.Report.Updates
		label := i18n.T(lang, "updates.row", s.Name, u.Total, u.Security)
		if u.RebootRequired {
			label += i18n.T(lang, "updates.reboot")
		}
		rows = append(rows, []models.InlineKeyboardButton{btn(label, fmt.Sprintf("pkg:%d:u", s.ID))})
	}
	text := i18n.T(lang, "updates.header") + "\n\n"
	if len(rows) == 0 {
		text += i18n.T(lang, "updates.none")
	} else {
		text += i18n.T(lang, "updates.hint")
	}
	rows = append(rows, menuRow(lang))
	return text, rows
}

// originProblems marks a card opened from Require attention, so Back returns there.
const originProblems = "p"

func (b *Bot) cardScreen(lang string, id int64, origin string) (string, keyboard, bool) {
	s, err := b.hub.store.Server(id)
	if err != nil {
		return "", nil, false
	}
	now := time.Now()
	sid := strconv.FormatInt(id, 10)
	back, suffix := cbList, ""
	if origin == originProblems {
		back, suffix = cbProblems, ":"+originProblems
	}
	rows := keyboard{{btn(i18n.T(lang, "btn.packages"), "pkg:"+sid)}}
	// ":c" marks mute buttons on a card, which is redrawn after muting.
	if s.MutedUntil.After(now) {
		rows = append(rows, []models.InlineKeyboardButton{btn(i18n.T(lang, "btn.unmute"), "mute:"+sid+":0:c"+suffix)})
	} else {
		rows = append(rows,
			[]models.InlineKeyboardButton{btn(i18n.T(lang, "btn.mute_1h"), "mute:"+sid+":1:c"+suffix)},
			[]models.InlineKeyboardButton{btn(i18n.T(lang, "btn.mute_24h"), "mute:"+sid+":24:c"+suffix)},
		)
	}
	rows = append(rows,
		[]models.InlineKeyboardButton{btn(i18n.T(lang, "btn.delete"), "del:"+sid)},
		[]models.InlineKeyboardButton{btn(i18n.T(lang, "btn.back"), back), btn(i18n.T(lang, "btn.menu"), cbMenu)},
	)
	return b.hub.Card(lang, s, now), rows, true
}

// callbackID parses the numeric server ID after the given prefix.
func callbackID(data, prefix string) (int64, bool) {
	idStr, _, _ := strings.Cut(strings.TrimPrefix(data, prefix), ":")
	id, err := strconv.ParseInt(idStr, 10, 64)
	return id, err == nil
}

// ---------------------------------------------------------------- handlers

func (b *Bot) onStart(ctx context.Context, _ *tg.Bot, u *models.Update) {
	b.clearAwaiting(u.Message.Chat.ID)
	text, rows := b.menuScreen(b.hub.Lang())
	b.send(ctx, u.Message.Chat.ID, text, rows)
}

func (b *Bot) onMenu(ctx context.Context, _ *tg.Bot, u *models.Update) {
	b.clearAwaiting(callbackChat(u))
	text, rows := b.menuScreen(b.hub.Lang())
	b.show(ctx, u, text, rows)
}

func (b *Bot) onList(ctx context.Context, _ *tg.Bot, u *models.Update) {
	text, rows := b.listScreen(b.hub.Lang())
	b.show(ctx, u, text, rows)
}

func (b *Bot) onProblems(ctx context.Context, _ *tg.Bot, u *models.Update) {
	text, rows := b.problemsScreen(b.hub.Lang())
	b.show(ctx, u, text, rows)
}

func (b *Bot) onUpdates(ctx context.Context, _ *tg.Bot, u *models.Update) {
	text, rows := b.updatesScreen(b.hub.Lang())
	b.show(ctx, u, text, rows)
}

func (b *Bot) onSettings(ctx context.Context, _ *tg.Bot, u *models.Update) {
	lang := b.hub.Lang()
	other := "ru"
	if lang == "ru" {
		other = "en"
	}
	b.show(ctx, u, b.hub.Settings(lang), keyboard{
		{btn(i18n.T(lang, "btn.lang", i18n.T(other, "lang.name")), cbLang)},
		menuRow(lang),
	})
}

// onLang switches between the two languages and redraws the settings screen.
func (b *Bot) onLang(ctx context.Context, api *tg.Bot, u *models.Update) {
	next := "ru"
	if b.hub.Lang() == "ru" {
		next = "en"
	}
	if err := b.hub.store.SetSetting("lang", next); err != nil {
		slog.Error("set lang", "err", err)
	}
	b.onSettings(ctx, api, u)
}

func (b *Bot) onAdd(ctx context.Context, _ *tg.Bot, u *models.Update) {
	lang := b.hub.Lang()
	b.show(ctx, u, i18n.T(lang, "add.prompt"), keyboard{
		{btn(i18n.T(lang, "btn.default_name"), cbAddAuto)},
		{btn(i18n.T(lang, "btn.cancel"), cbMenu)},
	})
	if m := u.CallbackQuery.Message.Message; m != nil {
		b.mu.Lock()
		b.awaitingName[m.Chat.ID] = m.ID
		b.mu.Unlock()
	}
}

func (b *Bot) onAddAuto(ctx context.Context, _ *tg.Bot, u *models.Update) {
	chatID := callbackChat(u)
	b.clearAwaiting(chatID)
	text, rows := b.addServer(b.hub.Lang(), "")
	b.show(ctx, u, text, rows)
}

// onText receives a new server's name after Add server; other text is ignored.
func (b *Bot) onText(ctx context.Context, _ *tg.Bot, u *models.Update) {
	if u.Message == nil || u.Message.Text == "" {
		return
	}
	chatID := u.Message.Chat.ID
	b.mu.Lock()
	promptID, waiting := b.awaitingName[chatID]
	delete(b.awaitingName, chatID)
	b.mu.Unlock()
	if !waiting {
		return
	}
	text, rows := b.addServer(b.hub.Lang(), u.Message.Text)
	// Drop the buttons from the prompt and answer below the typed name.
	b.editMessage(ctx, chatID, promptID, i18n.T(b.hub.Lang(), "add.prompt"), nil)
	b.send(ctx, chatID, text, rows)
}

func (b *Bot) addServer(lang, name string) (string, keyboard) {
	srv, cmd, err := b.hub.AddServer(name)
	if err != nil {
		slog.Error("add server", "err", err)
		return i18n.T(lang, "bot.error"), keyboard{menuRow(lang)}
	}
	return i18n.T(lang, "add.done", html.EscapeString(srv.Name), html.EscapeString(cmd)),
		keyboard{{btn(i18n.T(lang, "btn.servers"), cbList), btn(i18n.T(lang, "btn.menu"), cbMenu)}}
}

func (b *Bot) clearAwaiting(chatID int64) {
	b.mu.Lock()
	delete(b.awaitingName, chatID)
	b.mu.Unlock()
}

func (b *Bot) onCard(ctx context.Context, _ *tg.Bot, u *models.Update) {
	lang := b.hub.Lang()
	id, ok := callbackID(u.CallbackQuery.Data, "srv:")
	parts := strings.Split(u.CallbackQuery.Data, ":")
	origin := ""
	if len(parts) == 3 {
		origin = parts[2]
	}
	text, rows, found := b.cardScreen(lang, id, origin)
	if !ok || !found {
		b.toast(ctx, u, i18n.T(lang, "bot.not_found"))
		return
	}
	if origin == "n" {
		_, _ = b.api.AnswerCallbackQuery(ctx, &tg.AnswerCallbackQueryParams{CallbackQueryID: u.CallbackQuery.ID})
		b.send(ctx, callbackChat(u), text, rows)
		return
	}
	b.show(ctx, u, text, rows)
}

func (b *Bot) onPackages(ctx context.Context, _ *tg.Bot, u *models.Update) {
	lang := b.hub.Lang()
	id, ok := callbackID(u.CallbackQuery.Data, "pkg:")
	s, err := b.hub.store.Server(id)
	if !ok || err != nil {
		b.toast(ctx, u, i18n.T(lang, "bot.not_found"))
		return
	}
	// ":u" means the list was opened from the Updates screen, so Back returns there.
	back := fmt.Sprintf("srv:%d", id)
	if strings.HasSuffix(u.CallbackQuery.Data, ":u") {
		back = cbUpdates
	}
	b.show(ctx, u, b.hub.Packages(lang, s), keyboard{{btn(i18n.T(lang, "btn.back"), back), btn(i18n.T(lang, "btn.menu"), cbMenu)}})
}

func (b *Bot) onMute(ctx context.Context, _ *tg.Bot, u *models.Update) {
	lang := b.hub.Lang()
	parts := strings.Split(u.CallbackQuery.Data, ":")
	if len(parts) < 3 {
		b.toast(ctx, u, "")
		return
	}
	id, err1 := strconv.ParseInt(parts[1], 10, 64)
	hours, err2 := strconv.Atoi(parts[2])
	srv, err3 := b.hub.store.Server(id)
	if err1 != nil || err2 != nil || err3 != nil {
		b.toast(ctx, u, i18n.T(lang, "bot.not_found"))
		return
	}
	until := time.Now().Add(time.Duration(hours) * time.Hour)
	if err := b.hub.store.Mute(id, until); err != nil {
		slog.Error("mute", "err", err)
		return
	}
	if hours == 0 {
		b.toast(ctx, u, i18n.T(lang, "bot.unmuted", srv.Name))
	} else {
		b.toast(ctx, u, i18n.T(lang, "bot.muted", srv.Name, until.In(b.hub.cfg.loc).Format("02.01 15:04")))
	}
	if len(parts) >= 4 && parts[3] == "c" {
		origin := ""
		if len(parts) == 5 {
			origin = parts[4]
		}
		if text, rows, ok := b.cardScreen(lang, id, origin); ok {
			if m := u.CallbackQuery.Message.Message; m != nil {
				b.editMessage(ctx, m.Chat.ID, m.ID, text, rows)
			}
		}
	}
}

func (b *Bot) onDelete(ctx context.Context, _ *tg.Bot, u *models.Update) {
	lang := b.hub.Lang()
	id, ok := callbackID(u.CallbackQuery.Data, "del:")
	srv, err := b.hub.store.Server(id)
	if !ok || err != nil {
		b.toast(ctx, u, i18n.T(lang, "bot.not_found"))
		return
	}
	b.show(ctx, u, i18n.T(lang, "delete.confirm", html.EscapeString(srv.Name)), keyboard{{
		btn(i18n.T(lang, "btn.yes_delete"), fmt.Sprintf("delok:%d", id)),
		btn(i18n.T(lang, "btn.cancel"), fmt.Sprintf("srv:%d", id)),
	}})
}

func (b *Bot) onDeleteConfirm(ctx context.Context, _ *tg.Bot, u *models.Update) {
	lang := b.hub.Lang()
	id, ok := callbackID(u.CallbackQuery.Data, "delok:")
	srv, err := b.hub.store.Server(id)
	if !ok || err != nil {
		b.toast(ctx, u, i18n.T(lang, "bot.not_found"))
		return
	}
	if err := b.hub.store.DeleteServer(id); err != nil {
		slog.Error("delete server", "err", err)
		return
	}
	b.show(ctx, u, i18n.T(lang, "delete.done", html.EscapeString(srv.Name)),
		keyboard{{btn(i18n.T(lang, "btn.servers"), cbList), btn(i18n.T(lang, "btn.menu"), cbMenu)}})
}
