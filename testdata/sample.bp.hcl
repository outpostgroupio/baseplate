provider "roblox" {
  api_key = env("BP_TEST_KEY")
}

resource "mock_thing" "a" {
  name = "root"
}

resource "mock_child" "c" {
  name   = "child"
  parent = mock_thing.a
}
