package crasher

func Crasher() {
	var b *bool
	println(!*b)
}