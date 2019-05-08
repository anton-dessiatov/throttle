package app

// ForwarderBufSize is the size in bytes of a buffer allocated for each end of
// a tunnel. One thing to mention is that each rate.Limiter's burst size must
// be greater or equal to this value.
const ForwarderBufSize = 64 * 1024
