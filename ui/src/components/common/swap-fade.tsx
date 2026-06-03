import { AnimatePresence, m } from 'motion/react'

import { fade } from '@/lib/motion'

// Cross-fades between mutually-exclusive states (typically a skeleton and its loaded
// content) so data settles in instead of popping. Drive it by passing a stable `id`
// per state — when `id` changes, the old node fades out and the new one fades in.
//
//   <SwapFade id={isLoading ? 'loading' : 'ready'}>
//     {isLoading ? <Skeleton/> : <Content/>}
//   </SwapFade>
//
// `initial={false}` skips the very first fade so the skeleton appears immediately
// (and doesn't double-animate under the page's own entrance transition); only the
// state *change* cross-fades. `mode="wait"` lets the outgoing node finish leaving
// before the incoming one enters, which keeps height changes from overlapping.
export function SwapFade({ id, children }: { id: string; children: React.ReactNode }) {
  return (
    <AnimatePresence mode="wait" initial={false}>
      <m.div key={id} variants={fade} initial="hidden" animate="visible" exit="exit">
        {children}
      </m.div>
    </AnimatePresence>
  )
}
