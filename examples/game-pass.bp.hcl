provider "roblox" {
  api_key = env("ROBLOX_API_KEY")
}

resource "roblox_experience" "main" {
  universe_id     = "1234567890"
  desktop_enabled = true
}

resource "roblox_game_pass" "vip" {
  universe_id = roblox_experience.main.id # resolved at apply time
  name        = "VIP"
  price       = 100                        # Robux (required)
  description = "Unlocks the VIP area"
  icon_file   = "./assets/vip.png"         # optional
  is_for_sale = true
}
