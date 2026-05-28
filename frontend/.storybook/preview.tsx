import type { Preview } from '@storybook/react-vite'
import { ChatStoreProvider } from '../src/state/ChatStoreProvider'
import '../src/index.css'

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
    }
  },
  decorators: [
    (Story) => (
      <ChatStoreProvider>
        <Story />
      </ChatStoreProvider>
    ),
  ],
};

export default preview;