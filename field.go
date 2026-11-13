package netsec

type primeField struct {
	p int64
}

func (f primeField) norm(v int64) int64 {
	v %= f.p
	if v < 0 {
		v += f.p
	}
	return v
}

func (f primeField) add(a, b int64) int64 {
	return f.norm(a + b)
}

func (f primeField) sub(a, b int64) int64 {
	return f.norm(a - b)
}

func (f primeField) mul(a, b int64) int64 {
	return f.norm(a * b)
}

func (f primeField) pow(a int64, exponent int) int64 {
	base := f.norm(a)
	result := int64(1)
	for exponent > 0 {
		if exponent&1 == 1 {
			result = f.mul(result, base)
		}
		base = f.mul(base, base)
		exponent >>= 1
	}
	return result
}

func (f primeField) inv(a int64) int64 {
	a = f.norm(a)
	if a == 0 {
		panic("netsec: inversion of zero")
	}
	return f.pow(a, int(f.p-2))
}

func isPrime(n int) bool {
	if n < 2 {
		return false
	}
	if n%2 == 0 {
		return n == 2
	}
	for divisor := 3; divisor <= n/divisor; divisor += 2 {
		if n%divisor == 0 {
			return false
		}
	}
	return true
}
