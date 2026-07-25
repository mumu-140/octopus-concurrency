"use client"

import { motion } from "motion/react"
import { cn } from "@/lib/utils"
import { useNavStore, type NavItem } from "@/components/modules/navbar"
import { ROUTES } from "@/route/config"
import { usePreload } from "@/route/use-preload"
import { ENTRANCE_VARIANTS } from "@/lib/animations/fluid-transitions"
import { useTranslations } from "next-intl"

export function NavBar() {
    const { activeItem, setActiveItem } = useNavStore()
    const { preload } = usePreload()
    const t = useTranslations('navbar')

    return (
        <div className="relative z-50 md:min-h-screen">
            <motion.nav
                aria-label={t('navigation')}
                className={cn(
                    "fixed bottom-2 left-1/2 max-w-[calc(100vw-1rem)] -translate-x-1/2 flex items-center gap-1 overflow-x-auto p-2",
                    "md:sticky md:top-30 md:left-auto md:bottom-auto md:max-w-none md:translate-x-0 md:flex-col md:gap-3 md:overflow-visible md:p-3",
                    "bg-sidebar text-sidebar-foreground border border-sidebar-border rounded-3xl",
                    "custom-shadow"
                )}
                variants={ENTRANCE_VARIANTS.navbar}
                initial="initial"
                animate="animate"
            >
                {ROUTES.map((route, index) => {
                    const isActive = activeItem === route.id
                    return (
                        <motion.button
                            key={route.id}
                            type="button"
                            onClick={() => setActiveItem(route.id as NavItem)}
                            onMouseEnter={() => preload(route.id)}
                            aria-label={t(route.id)}
                            title={t(route.id)}
                            aria-current={isActive ? 'page' : undefined}
                            className={cn(
                                "relative flex size-11 shrink-0 items-center justify-center rounded-2xl z-20 md:size-auto md:p-3",
                                isActive ? "text-sidebar-primary-foreground" : "text-sidebar-foreground/60 hover:bg-sidebar-accent"
                            )}
                            initial={{ opacity: 0, scale: 0.8 }}
                            animate={{
                                opacity: 1,
                                scale: 1,
                                transition: {
                                    delay: index * 0.05,
                                    duration: 0.3,
                                }
                            }}
                            whileHover={{ scale: 1.1, zIndex: 30 }}
                            whileTap={{ scale: 0.95 }}
                        >
                            {isActive && (
                                <motion.div
                                    layoutId="navbar-indicator"
                                    className="absolute inset-0 bg-sidebar-primary rounded-2xl z-0"
                                    transition={{ type: "spring", stiffness: 300, damping: 30 }}
                                />
                            )}
                            <span className="relative z-10">
                                <route.icon className="size-5 md:size-6" strokeWidth={2} />
                            </span>
                        </motion.button>
                    )
                })}
            </motion.nav>
        </div>
    )
}
