provider "roblox" {
  api_key = env("ROBLOX_API_KEY")
}

resource "roblox_experience" "main" {
  universe_id     = "10438196190"
  desktop_enabled = true
}

# A live-ops event. Needs the universe.event:read + universe.event:write scopes.
resource "roblox_experience_event" "double_xp" {
  universe_id = roblox_experience.main.id # resolved at apply time
  title       = "Double XP Weekend"
  subtitle    = "Twice the XP, all weekend"
  description = "Every quest and challenge awards double XP."

  # RFC 3339; any offset works. Compared and stored in UTC, whole seconds.
  # The start must be in the future; the event must last at least 15 minutes.
  start_time = "2026-12-04T18:00:00Z"
  end_time   = "2026-12-06T18:00:00-08:00"

  visibility = "private" # "public" or "private"

  # Optional:
  # place_id            = "123456789"     # venue place in the universe
  # group_id            = "1234567"       # host group (create-only)
  # thumbnail_media_ids = ["987654321"]   # image asset ids; the first is the primary
  # featuring_enabled   = true
  # tagline             = "XP frenzy"     # can't be cleared once set
}
