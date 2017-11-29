package templates

import (
	"bytes"
	"github.com/jonas747/discordgo"
	"github.com/jonas747/yagpdb/common"
	"github.com/mediocregopher/radix.v2/redis"
	"github.com/pkg/errors"
	"strings"
	"text/template"
)

var (
	StandardFuncMap = map[string]interface{}{
		"dict":      Dictionary,
		"in":        in,
		"title":     strings.Title,
		"add":       add,
		"roleAbove": roleIsAbove,
		"adjective": common.RandomAdjective,
		"randInt":   randInt,
		"shuffle":   shuffle,
		"seq":       sequence,
		"joinStr":   joinStrings,
		"str":       str,
		"lower":     strings.ToLower,
		"toString":  tmplToString,
		"toInt":     tmplToInt,
		"toInt64":   tmplToInt64,
	}

	contextSetupFuncs = []ContextSetupFunc{
		baseContextFuncs,
	}
)

func TODO() {}

type ContextSetupFunc func(ctx *Context)

func RegisterSetupFunc(f ContextSetupFunc) {
	contextSetupFuncs = append(contextSetupFuncs, f)
}

type Context struct {
	Guild   *discordgo.Guild
	Channel *discordgo.Channel

	Member *discordgo.Member
	Msg    *discordgo.Message

	BotUser *discordgo.User

	ContextFuncs map[string]interface{}
	Data         map[string]interface{}
	Redis        *redis.Client

	MentionEveryone  bool
	MentionHere      bool
	MentionRoles     []string
	MentionRoleNames []string

	SentDM bool
}

func NewContext(botUser *discordgo.User, g *discordgo.Guild, c *discordgo.Channel, member *discordgo.Member) *Context {
	ctx := &Context{
		Guild:   g,
		Channel: c,

		BotUser: botUser,
		Member:  member,

		ContextFuncs: make(map[string]interface{}),
		Data:         make(map[string]interface{}),
	}

	ctx.setupContextFuncs()

	return ctx
}

func (c *Context) setupContextFuncs() {
	for _, f := range contextSetupFuncs {
		f(c)
	}
}

func (c *Context) setupBaseData() {

	if c.Guild != nil {
		c.Data["Guild"] = c.Guild
		c.Data["Server"] = c.Guild
		c.Data["server"] = c.Guild
	}

	if c.Channel != nil {
		c.Data["Channel"] = c.Channel
		c.Data["channel"] = c.Channel
	}

	if c.Member != nil {
		c.Data["Member"] = c.Member
		c.Data["User"] = c.Member.User
		c.Data["user"] = c.Member.User
	}
}

func (c *Context) Execute(redisClient *redis.Client, source string) (string, error) {
	if c.Msg == nil {
		// Construct a fake message
		c.Msg = new(discordgo.Message)
		c.Msg.Author = c.BotUser
		c.Msg.ChannelID = c.Guild.ID
	}

	c.setupBaseData()

	c.Redis = redisClient

	tmpl := template.New("")
	tmpl.Funcs(StandardFuncMap)
	tmpl.Funcs(c.ContextFuncs)

	parsed, err := tmpl.Parse(source)
	if err != nil {
		return "", errors.WithMessage(err, "Failed parsing template")
	}

	var buf bytes.Buffer
	err = parsed.Execute(&buf, c.Data)

	result := common.EscapeSpecialMentionsConditional(buf.String(), c.MentionEveryone, c.MentionHere, c.MentionRoles)
	if err != nil {
		return result, errors.WithMessage(err, "Failed execuing template")
	}

	return result, nil
}

func baseContextFuncs(c *Context) {
	c.ContextFuncs["sendDM"] = tmplSendDM(c)
	c.ContextFuncs["mentionEveryone"] = tmplMentionEveryone(c)
	c.ContextFuncs["mentionHere"] = tmplMentionHere(c)
	c.ContextFuncs["mentionRoleName"] = tmplMentionRoleName(c)
	c.ContextFuncs["mentionRoleID"] = tmplMentionRoleID(c)
	c.ContextFuncs["hasRoleName"] = tmplHasRoleName(c)
	c.ContextFuncs["hasRoleID"] = tmplHasRoleID(c)
}
