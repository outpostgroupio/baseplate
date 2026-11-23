provider "roblox" {
  api_key = env("ROBLOX_API_KEY")
}

resource "roblox_experience" "main" {
  universe_id     = "1234567890"
  desktop_enabled = true
}

resource "roblox_badge" "first_login" {
  universe_id = roblox_experience.main.id # resolved at apply time
  name        = "Welcome!"
  description = "Awarded on your first visit"
  enabled     = true
  icon_file   = "./assets/welcome.png" # required; change detected via content hash

  # Opt-in acknowledgement of the Robux cost to create a badge (free in practice).
  expected_cost  = 0
  payment_source = "user" # "user" or "group"; routes funds when expected_cost > 0
}
