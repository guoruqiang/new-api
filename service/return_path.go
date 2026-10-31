package service

import "github.com/QuantumNous/new-api/setting/system_setting"

func PaymentReturnURL(suffix string) string {
	return system_setting.GetConsoleAddress() + suffix
}
