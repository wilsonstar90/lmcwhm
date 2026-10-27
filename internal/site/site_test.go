package site

import (
	"reflect"
	"testing"
)

func TestCertNames(t *testing.T) {
	cases := []struct{ in, want []string }{
		{[]string{"a.com", "www.a.com"}, []string{"a.com", "www.a.com"}},
		// The reported failure: www is covered by *.a.com.
		{[]string{"a.com", "www.a.com", "*.a.com"}, []string{"a.com", "*.a.com"}},
		// Deeper names and other domains are not covered.
		{[]string{"a.com", "x.y.a.com", "b.com", "*.a.com"}, []string{"a.com", "x.y.a.com", "b.com", "*.a.com"}},
		{[]string{"a.com", "*.a.com", "*.a.com", "a.com"}, []string{"a.com", "*.a.com"}},
	}
	for _, c := range cases {
		if got := certNames(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("certNames(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}
