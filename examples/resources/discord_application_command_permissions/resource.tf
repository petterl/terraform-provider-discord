# Writing needs a Bearer token from the application that owns the command —
# another application's is refused, even with the same owner. Its owner must be
# allowed to manage the server and its roles. Reading works with the bot token
# alone, so plan needs no credentials.
provider "discord" {
  alias     = "report_bot"
  token     = var.discord_token
  client_id = var.report_bot_client_id
  secret    = var.report_bot_client_secret
}

# Hide /report from everyone but moderators and one channel.
resource "discord_application_command_permissions" "report" {
  provider = discord.report_bot

  server_id      = var.server_id
  application_id = var.bot_application_id
  command_id     = var.report_command_id

  permission {
    type  = "role"
    id    = var.server_id # @everyone
    allow = false
  }

  permission {
    type  = "role"
    id    = var.moderator_role_id
    allow = true
  }

  permission {
    type  = "channel"
    id    = var.mod_channel_id
    allow = true
  }
}
