provider "roblox" {
  api_key = env("ROBLOX_API_KEY")
}

resource "roblox_experience" "main" {
  universe_id     = "10438196190"
  desktop_enabled = true
}

# Page on-call when the client crash rate on phones/tablets stays above 3% for
# 15 consecutive minutes, evaluated separately per platform.
resource "roblox_analytics_alert" "mobile_crash_rate" {
  universe_id             = roblox_experience.main.id # resolved at apply time
  name                    = "Mobile crash rate"
  description             = "Crash rate above 3% on phones or tablets"
  metric                  = "ClientCrashRate15m"
  severity                = "SEV_0"
  interval                = "OneMinute"
  consecutive_occurrences = 15 # OneMinute allows 6-30
  operator                = "Gt"
  threshold               = 3

  filters = {
    Platform = ["Phone", "Tablet"]
  }
  breakdown_dimension = "Platform" # must also be a filters key

  # Webhooks can only be created in the Creator Dashboard; paste their UUIDs here.
  # webhook_configuration_ids = ["3f2b8c1e-5d4a-4f6b-9a7e-1c2d3e4f5a6b"]
}

# Warn when peak concurrent players drop more than 30% versus the same day last week.
resource "roblox_analytics_alert" "pcu_drop" {
  universe_id              = roblox_experience.main.id
  name                     = "PCU week-over-week drop"
  metric                   = "PeakConcurrentPlayers"
  severity                 = "SEV_1"
  interval                 = "OneDay"
  consecutive_occurrences  = 1
  operator                 = "Lt"
  threshold                = -30 # percent change
  evaluation_mode          = "PeriodOverPeriod"
  period_offset_multiplier = 7
}
