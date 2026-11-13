// hls.js ships types for its full build but not its light build. The light
// build has the same API, minus features Flick doesn't use, so reuse the full
// build's types for it.
declare module 'hls.js/light' {
  export * from 'hls.js'
  export { default } from 'hls.js'
}
