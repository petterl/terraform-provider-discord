package discord

import (
	"context"
	"encoding/json"

	"github.com/bwmarrin/discordgo"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
)

// autoArchiveDurations are the only inactivity windows, in minutes, Discord
// accepts for a thread: 1 hour, 1 day, 3 days, 1 week.
var autoArchiveDurations = []int{60, 1440, 4320, 10080}

// defaultAutoArchiveDurationSchema is the channel-level default that new
// threads in a text, news or forum channel start with. Computed, because
// Discord reports a value even when none was ever set, and leaving the
// attribute out of the config should not plan a change.
func defaultAutoArchiveDurationSchema() *schema.Schema {
	return &schema.Schema{
		Type:         schema.TypeInt,
		Optional:     true,
		Computed:     true,
		Description:  "Minutes of inactivity after which new threads in this channel are archived, unless the thread's creator picks another value. Discord accepts `60`, `1440`, `4320` or `10080` (1 week, the maximum). Existing threads keep their own value.",
		ValidateFunc: validation.IntInSlice(autoArchiveDurations),
	}
}

// channelHasThreads reports whether a channel type carries the attribute.
func channelHasThreads(channelType string) bool {
	switch channelType {
	case "text", "news", "forum":
		return true
	}
	return false
}

// channelThreadDefaults is sent and read as raw JSON because discordgo v0.29's
// Channel and ChannelEdit have no field for it.
type channelThreadDefaults struct {
	DefaultAutoArchiveDuration *int `json:"default_auto_archive_duration,omitempty"`
}

// applyDefaultAutoArchiveDuration PATCHes the channel's thread default when it
// is configured — on create always, on update only when it changed.
func applyDefaultAutoArchiveDuration(ctx context.Context, client *discordgo.Session, channelID string, d *schema.ResourceData, isUpdate bool) error {
	if !channelHasThreads(d.Get("type").(string)) {
		return nil
	}
	v, ok := d.GetOk("default_auto_archive_duration")
	if !ok || (isUpdate && !d.HasChange("default_auto_archive_duration")) {
		return nil
	}
	minutes := v.(int)
	endpoint := discordgo.EndpointChannel(channelID)
	_, err := client.RequestWithBucketID("PATCH", endpoint, channelThreadDefaults{DefaultAutoArchiveDuration: &minutes}, endpoint, discordgo.WithContext(ctx))
	return err
}

// readDefaultAutoArchiveDuration copies the value from a raw channel object
// into state. A channel object without the field leaves state as it was.
func readDefaultAutoArchiveDuration(body []byte, d *schema.ResourceData) error {
	var resp channelThreadDefaults
	if err := json.Unmarshal(body, &resp); err != nil {
		return err
	}
	if resp.DefaultAutoArchiveDuration != nil {
		d.Set("default_auto_archive_duration", *resp.DefaultAutoArchiveDuration)
	}
	return nil
}
