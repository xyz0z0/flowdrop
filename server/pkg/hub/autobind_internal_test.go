package hub

// Internal coverage for the mDNS advertisement filter: container/VM bridge
// listeners are bound on purpose (LocalSend parity) but must never be published
// on the LAN, because a phone that resolved 172.17.0.1 cannot reach it.

import (
	"reflect"
	"testing"

	"flowdrop/internal/netbind"
)

func TestFilterAdvertised(t *testing.T) {
	cases := []struct {
		name  string
		addrs []string
		kinds map[string]netbind.Kind
		want  []string
	}{
		{
			name: "container bridges dropped, order preserved",
			addrs: []string{
				"127.0.0.1:8080",
				"192.168.31.184:8080",
				"100.125.168.63:8080",
				"172.17.0.1:8080",
				"172.18.0.1:8080",
			},
			kinds: map[string]netbind.Kind{
				"192.168.31.184:8080": netbind.KindLAN,
				"100.125.168.63:8080": netbind.KindTailscale,
				"172.17.0.1:8080":     netbind.KindVirtual,
				"172.18.0.1:8080":     netbind.KindVirtual,
			},
			want: []string{
				"127.0.0.1:8080",
				"192.168.31.184:8080",
				"100.125.168.63:8080",
			},
		},
		{
			name:  "unknown addresses kept (primary and explicit additional listens)",
			addrs: []string{"127.0.0.1:8080", "10.0.0.5:8080"},
			kinds: nil,
			want:  []string{"127.0.0.1:8080", "10.0.0.5:8080"},
		},
		{
			name:  "only virtual bridges left means nothing is advertised",
			addrs: []string{"172.17.0.1:8080", "172.18.0.1:8080"},
			kinds: map[string]netbind.Kind{
				"172.17.0.1:8080": netbind.KindVirtual,
				"172.18.0.1:8080": netbind.KindVirtual,
			},
			want: []string{},
		},
		{
			name:  "empty input returns nil",
			addrs: nil,
			kinds: map[string]netbind.Kind{"172.17.0.1:8080": netbind.KindVirtual},
			want:  nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := filterAdvertised(tc.addrs, tc.kinds)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("filterAdvertised(%#v) = %#v, want %#v", tc.addrs, got, tc.want)
			}
		})
	}
}
