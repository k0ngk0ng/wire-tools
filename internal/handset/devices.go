package handset

// Match explicit USB identities; never claim arbitrary keyboard media keys.
func usbDeviceName(vendor, product string) string {
	switch vendor + ":" + product {
	case "001f:0b23":
		return "AB17X USB Audio"
	case "0c76:1203":
		return "USB PnP Audio Device"
	case "12d1:3a07":
		return "SPACETOUCH USB Audio"
	}
	return ""
}
