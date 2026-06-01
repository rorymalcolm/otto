package otto

import (
	"testing"
)

func TestForOf(t *testing.T) {
	tt(t, func() {
		test, _ := test()

		// Iterating an array yields its values, not its indices.
		test(`
            var s = 0;
            for (var x of [1, 2, 3]) s += x;
            s;
        `, 6)

		// for (let ...) of.
		test(`
            var s = 0;
            for (let x of [10, 20]) s += x;
            s;
        `, 30)

		// Iterating a string yields its characters.
		test(`
            var r = "";
            for (const c of "abc") r += c + ".";
            r;
        `, "a.b.c.")

		// const loop variable.
		test(`
            var sum = 0;
            for (const n of [1, 2, 3, 4]) sum += n;
            sum;
        `, 10)
	})
}

func TestForOfControlFlow(t *testing.T) {
	tt(t, func() {
		test, _ := test()

		test(`
            var out = [];
            for (let x of [1, 2, 3, 4]) {
                if (x === 2) continue;
                if (x === 4) break;
                out.push(x);
            }
            out.join(",");
        `, "1,3")
	})
}

func TestForOfPerIteration(t *testing.T) {
	tt(t, func() {
		test, _ := test()

		// Each iteration of for (let ... of) has its own binding.
		test(`
            var fns = [];
            for (let x of [1, 2, 3]) fns.push(function() { return x; });
            [fns[0](), fns[1](), fns[2]()].join(",");
        `, "1,2,3")
	})
}
