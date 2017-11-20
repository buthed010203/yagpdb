package bot

import (
	"context"
	"errors"
	"github.com/Sirupsen/logrus"
	"github.com/jonas747/discordgo"
	"github.com/jonas747/yagpdb/bot/eventsystem"
	"github.com/jonas747/yagpdb/common"
	"github.com/mediocregopher/radix.v2/redis"
	"github.com/patrickmn/go-cache"
	"time"
)

var (
	Cache = cache.New(time.Minute, time.Minute)
)

func ContextSession(ctx context.Context) *discordgo.Session {
	return ctx.Value(common.ContextKeyDiscordSession).(*discordgo.Session)
}

func ContextRedis(ctx context.Context) *redis.Client {
	return ctx.Value(common.ContextKeyRedis).(*redis.Client)
}

func RedisWrapper(inner eventsystem.Handler) eventsystem.Handler {
	return func(evt *eventsystem.EventData) {
		r, err := common.RedisPool.Get()
		if err != nil {
			logrus.WithError(err).WithField("evt", evt.Type.String()).Error("Failed retrieving redis client")
			return
		}

		defer func() {
			common.RedisPool.Put(r)
		}()

		inner(evt.WithContext(context.WithValue(evt.Context(), common.ContextKeyRedis, r)))
	}
}

func GetCreatePrivateChannel(user string) (*discordgo.Channel, error) {

	// State.RLock()
	// defer State.RUnlock()

	// for _, c := range State.PrivateChannels {
	// 	if c.Recipient() != nil && c.Recipient().ID == user {
	// 		return c.Copy(true, false), nil
	// 	}
	// }

	channel, err := common.BotSession.UserChannelCreate(user)
	if err != nil {
		return nil, err
	}

	return channel, nil
}

func SendDM(user string, msg string) error {
	channel, err := GetCreatePrivateChannel(user)
	if err != nil {
		return err
	}

	_, err = common.BotSession.ChannelMessageSend(channel.ID, msg)
	return err
}

var (
	ErrStartingUp    = errors.New("Starting up, caches are being filled...")
	ErrGuildNotFound = errors.New("Guild not found")
)

func AdminOrPerm(needed int, userID, channelID string) (bool, error) {
	perms, err := MemberPermissions(nil, channelID, userID)
	if err != nil {
		return false, err
	}

	if perms&needed != 0 {
		return true, nil
	}

	if perms&discordgo.PermissionManageServer != 0 || perms&discordgo.PermissionAdministrator != 0 {
		return true, nil
	}

	return false, nil
}

// Calculates the permissions for a member.
// https://support.discordapp.com/hc/en-us/articles/206141927-How-is-the-permission-hierarchy-structured-
func MemberPermissions(g *discordgo.Guild, channelID string, memberID string) (apermissions int, err error) {
	var channel *discordgo.Channel
	if g == nil {
		channel, err = State.Channel(channelID)
		if err != nil {
			return 0, err
		}

		g, err = State.Guild(channel.GuildID)
		if err != nil {
			return 0, err
		}
	} else {
		for _, c := range g.Channels {
			if c.ID == channelID {
				channel = c
			}
		}

		if channel == nil {
			return 0, errors.New("Channel not found")
		}
	}

	if memberID == g.OwnerID {
		return discordgo.PermissionAll, nil
	}

	member, err := State.GuildMember(g.ID, memberID)
	if err != nil {
		return 0, err
	}

	for _, role := range g.Roles {
		if role.ID == g.ID {
			apermissions |= role.Permissions
			break
		}
	}

	for _, role := range g.Roles {
		for _, roleID := range member.Roles {
			if role.ID == roleID {
				apermissions |= role.Permissions
				break
			}
		}
	}

	// Administrator bypasses channel overrides
	if apermissions&discordgo.PermissionAdministrator == discordgo.PermissionAdministrator {
		apermissions |= discordgo.PermissionAll
		return
	}

	// Apply @everyone overrides from the channel.
	for _, overwrite := range channel.PermissionOverwrites {
		if g.ID == overwrite.ID {
			apermissions &= ^overwrite.Deny
			apermissions |= overwrite.Allow
			break
		}
	}

	denies := 0
	allows := 0

	// Member overwrites can override role overrides, so do two passes
	for _, overwrite := range channel.PermissionOverwrites {
		for _, roleID := range member.Roles {
			if overwrite.Type == "role" && roleID == overwrite.ID {
				denies |= overwrite.Deny
				allows |= overwrite.Allow
				break
			}
		}
	}

	apermissions &= ^denies
	apermissions |= allows

	for _, overwrite := range channel.PermissionOverwrites {
		if overwrite.Type == "member" && overwrite.ID == memberID {
			apermissions &= ^overwrite.Deny
			apermissions |= overwrite.Allow
			break
		}
	}

	if apermissions&discordgo.PermissionAdministrator == discordgo.PermissionAdministrator {
		apermissions |= discordgo.PermissionAllChannel
	}

	return
}
