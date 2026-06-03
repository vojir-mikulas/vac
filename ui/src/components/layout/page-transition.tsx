import { m } from 'motion/react'
import { useRouterState } from '@tanstack/react-router'

import { fadeUp } from '@/lib/motion'

// Wraps the routed page content so each navigation fades up into place instead of
// popping. Keyed on pathname: when the location changes, React remounts this node
// and replays the entrance.
//
// We deliberately use a keyed entrance rather than an AnimatePresence exit. The app
// shares a single <Outlet/>, so an exiting copy would re-render with the *next*
// route's content (a flash) — entrance-only is flash-free and just as smooth, since
// the incoming page occupies its space immediately at opacity 0 and settles in.
export function PageTransition({ children }: { children: React.ReactNode }) {
  const pathname = useRouterState({ select: (s) => s.location.pathname })

  return (
    <m.div key={pathname} variants={fadeUp} initial="hidden" animate="visible">
      {children}
    </m.div>
  )
}
