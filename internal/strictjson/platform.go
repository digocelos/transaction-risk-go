package strictjson

import "strconv"

// The toolkit supports 64-bit platforms only: counts and sums of integers
// up to MaxInteger, 2^53−1, are held in int. This declaration does not
// compile where int has 32 bits.
const _ uint = strconv.IntSize - 64
