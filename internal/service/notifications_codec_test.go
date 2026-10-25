package service

import "testing"

func TestNotificationIDAndCursorBinding(t *testing.T) {
	for _, v := range []string{"nf1_9007199254740993", "nf1_9223372036854775807"} {
		n, e := DecodeNotificationID(v)
		if e != nil {
			t.Fatal(e)
		}
		got, e := EncodeNotificationID(n)
		if e != nil || got != v {
			t.Fatal(got, e)
		}
	}
	for _, v := range []string{"nf1_0", "nf1_+1", "nf1_01", "nf1_1e3", "nf1_9223372036854775808", "e1.1"} {
		if _, e := DecodeNotificationID(v); e == nil {
			t.Fatal(v)
		}
	}
}
