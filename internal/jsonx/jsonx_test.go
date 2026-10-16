package jsonx

import "testing"

func TestLenient(t *testing.T) {
	var v struct {
		N, S, Z Int
		T, U, W Text
		X       int
	}
	err := Decode([]byte(`{"N":12,"S":"7","Z":"terminal_3","T":"hi","U":{"b":1,"a":[true,null]},"W":null,"X":"not a number"}`), &v)
	if err != nil {
		t.Fatal(err)
	}
	if v.N != 12 || v.S != 7 || v.Z != 0 || v.T != "hi" || v.U != `{"b":1,"a":[true,null]}` || v.W != "" || v.X != 0 {
		t.Errorf("%+v", v)
	}
	if Decode([]byte(`[1]`), &v) == nil {
		t.Error("not an object: an error")
	}
	if got := string(First([]byte("{\"a\":1}\n{\"b\":2}"))); got != `{"a":1}` {
		t.Errorf("First: %s", got)
	}
	if got := Quote("a\"\x01\t<"); got != `"a\"\u0001\t<"` {
		t.Errorf("Quote: %s", got)
	}
}
