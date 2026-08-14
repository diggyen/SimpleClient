//go:build !windows
// +build !windows

// PATCH (SimpleClient): Linux/macOS derlemesi için cliprdr stub'ı.
//
// Upstream cliprdr.go yalnızca Windows'ta tanımlı sembolleri kullandığı için o
// dosyaya "//go:build windows" eklendi. Ancak protocol/t125/mcs.go, Windows dışı
// platformlarda da cliprdr.ChannelName ve cliprdr.ChannelOption sabitlerine
// ihtiyaç duyuyor (SetClientCliprdr). Bu dosya yalnızca o iki sabiti sağlar;
// değerler upstream cliprdr.go ile birebir aynıdır.
//
// Pano yönlendirme (clipboard redirection) kiosk'ta hiçbir zaman
// etkinleştirilmiyor — yalnızca sanal kanal adı sunucuya bildiriliyor.

package cliprdr

import "github.com/tomatome/grdp/plugin"

const (
	// ChannelName is the MS-RDPECLIP static virtual channel name.
	ChannelName = plugin.CLIPRDR_SVC_CHANNEL_NAME

	// ChannelOption carries the channel initialisation flags.
	ChannelOption = plugin.CHANNEL_OPTION_INITIALIZED | plugin.CHANNEL_OPTION_ENCRYPT_RDP |
		plugin.CHANNEL_OPTION_COMPRESS_RDP | plugin.CHANNEL_OPTION_SHOW_PROTOCOL
)
