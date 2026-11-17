package controller

import "github.com/QuantumNous/new-api/setting/system_setting"

func paymentReturnPath(suffix string) string {
	return system_setting.GetConsoleAddress() + suffix
}
