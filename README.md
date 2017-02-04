# goreduce

[![Build Status](https://travis-ci.org/mvdan/goreduce.svg?branch=master)](https://travis-ci.org/mvdan/goreduce)

Reduce a function to its simplest form as long as it produces a compiler
error or any output (such as a panic) matching a regular expression.

Still a work in progress and barely useful.

### Example

```
func Crasher() {
        a := []int{1, 2, 3}
        if true {
                a = append(a, 4)
        }
        a[1] = -2
        println(a[10])
}
```

	$ goreduce -match 'index out of range' . Crasher

```
func Crasher() {
        a := []int{1, 2, 3}
        println(a[10])
}
```

### Rules

* Remove single statements: `{ a; b; }` -> `{ a; }`
* Bypass to if or else branches: `if a { b }` -> `b`
* Set literals to simple values: `123` -> `0`
* Remove slice expression parts: `a[1:2:3]` -> `a[1:]`
