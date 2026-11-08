package platform

import (
	"net/netip"
	"testing"
)

// Failure scenarios: LLINFO/WASCLONED host caches must not reserve virtual
// addresses; static host routes, live TUN routes and connected/cloning network
// routes must still reject overlaps. Scope/interface alone is not a filter.
// Unknown/missing flags retain the route; malformed destinations fail closed.
func TestDarwinNeighborCachesDoNotReserveVirtualAddresses(t *testing.T) {
	candidate := netip.MustParseAddr("100.113.200.14")
	cases := []struct {
		name, line string
		conflict   bool
	}{
		{"observed bridge ARP cache", "100.113.200.14 link#25 UHLWIg bridge100 !", false},
		{"resolved neighbor cache", "100.113.200.14 aa:bb:cc:dd:ee:ff UHLWIr en1 1180", false},
		{"LLINFO host cache", "100.113.200.14 link#25 UHL bridge100 !", false},
		{"WASCLONED host cache", "100.113.200.14 192.168.139.1 UHWIG bridge100 1180", false},
		{"static host route", "100.113.200.14 192.168.139.1 UGHS en1", true},
		{"static host with neighbor flags", "100.113.200.14 link#25 UHLWS bridge100 !", true},
		{"active point to point TUN", "100.113.200.14 100.113.200.13 UH utun7", true},
		{"ordinary scoped host route", "100.113.200.14 192.168.139.1 UHGIg bridge100", true},
		{"connected cloning network", "100.113.200/24 link#25 UCS bridge100 !", true},
		{"protocol cloning network", "100.113.200/24 link#25 Uc bridge100 !", true},
		{"nonhost cache flags do not hide networks", "100.113.200/24 link#25 ULW bridge100 !", true},
		{"missing flags stay conservative", "100.113.200.14 link#25", true},
		{"unrelated network", "192.168.138/23 link#25 UCS bridge100 !", false},
		{"default route", "default 192.168.139.1 UGScg bridge100", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := []byte("Routing tables\n\nInternet:\nDestination Gateway Flags Netif Expire\n" + tc.line + "\n")
			routes, err := parseDarwinRoutePrefixes(data)
			if err != nil {
				t.Fatal(err)
			}
			err = checkCandidateRoutes([]netip.Addr{candidate}, routes)
			if (err != nil) != tc.conflict {
				t.Fatalf("overlap=%v, want %v; route=%q; error=%v", err != nil, tc.conflict, tc.line, err)
			}
		})
	}
}

func TestDarwinNeighborCacheDoesNotHideRealNetworkConflict(t *testing.T) {
	data := []byte("Destination Gateway Flags Netif Expire\n100.113.200.14 link#25 UHLWIg bridge100 !\n100.113.200/24 link#25 UCS bridge100 !\n")
	routes, err := parseDarwinRoutePrefixes(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkCandidateRoutes([]netip.Addr{netip.MustParseAddr("100.113.200.14")}, routes); err == nil {
		t.Fatal("real network overlap was hidden by a neighbor-cache entry")
	}
}

func TestDarwinNeighborFlagsDoNotHideMalformedRoutes(t *testing.T) {
	if _, err := parseDarwinRoutePrefixes([]byte("100.113.999.14 link#25 UHLWIg bridge100 !\n")); err == nil {
		t.Fatal("malformed route accepted")
	}
}
