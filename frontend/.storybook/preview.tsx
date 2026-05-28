import type { Preview } from '@storybook/react-vite'
import { ChatStoreProvider } from '../src/state/ChatStoreProvider'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { initialize, mswLoader } from 'msw-storybook-addon'
import { mswHandlers } from './msw-handlers'
import '../src/index.css'

initialize({ onUnhandledRequest: 'error' })

const queryClient = new QueryClient({
  defaultOptions: { queries: { staleTime: 5000, refetchOnWindowFocus: false } }
})

const preview: Preview = {
  parameters: {
    controls: {
      matchers: {
        color: /(background|color)$/i,
        date: /Date$/i,
      },
    },
    a11y: {
      test: 'todo'
    },
    msw: { handlers: mswHandlers }
  },
  globalTypes: {
    theme: {
      description: 'Dark mode',
      defaultValue: 'dark',
      toolbar: {
        title: 'Theme',
        icon: 'paintbrush',
        items: ['dark', 'light'],
      },
    },
  },
  decorators: [
    (Story) => {
      return (
        <QueryClientProvider client={queryClient}>
          <MemoryRouter>
            <ChatStoreProvider>
              <Story />
            </ChatStoreProvider>
          </MemoryRouter>
        </QueryClientProvider>
      )
    },
  ],
  loaders: [mswLoader],
  async beforeEach({ globals }) {
    // Apply theme based on global type
    document.documentElement.classList.toggle('dark', globals.theme === 'dark')
    localStorage.setItem('theme', globals.theme)
  },
}

export default preview
