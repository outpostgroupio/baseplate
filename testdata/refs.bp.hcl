resource "mock_thing" "a" {
  name = "root"
}

resource "mock_ref" "b" {
  name   = "child"
  target = mock_thing.a.id
}
