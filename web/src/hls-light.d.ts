// This file gives the light build of hls.js the types of its full build.
// hls.js ships types only for its full build. The light build has the same
// API, minus features Flick doesn't use, so the same types fit.
declare module 'hls.js/light' {
  export * from 'hls.js'
  export { default } from 'hls.js'
}
