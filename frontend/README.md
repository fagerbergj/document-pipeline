# Frontend

React + TypeScript + Vite SPA for the document-pipeline project.

## Architecture

### Tech Stack

- **React 19** with TypeScript
- **Tailwind CSS** for styling (dark mode supported)
- **React Query** for data fetching and caching
- **React Router DOM 7** for navigation
- **Vite** for development and production builds
- **Vitest** for unit testing
- **@hey-api/openapi-ts** for TypeScript API client generation
- **Storybook** for component development and testing

### Project Structure

```
frontend/
├── src/
│   ├── api.ts                  # Thin wrapper around generated SDK
│   ├── App.tsx                 # Main router layout
│   ├── main.tsx                # App entry point
│   ├── index.css               # Global Tailwind setup
│   ├── components/             # Shared UI components
│   │   ├── AgentParts.tsx      # MessagePart rendering (text, thinking, tool_call, confirmation)
│   │   ├── DiffView.tsx        # Line-level diff visualization
│   │   ├── EditableOutput.tsx  # Readable/editable content with markdown support
│   │   ├── Sidebar.tsx         # Navigation sidebar
│   │   ├── SearchBar.tsx       # Global search input
│   │   └── ...                 # Other reusable components
│   ├── pages/                  # Top-level routes
│   │   ├── Chat.tsx            # Chat/RAG interface
│   │   ├── Document.tsx        # Document details + job live log
│   │   ├── Dashboard.tsx       # Document list + search
│   │   └── Contexts.tsx        # Context library management
│   ├── state/                  # App-level state management
│   │   ├── agentStream.ts      # SSE event dispatcher (shared between chat & jobs)
│   │   ├── chatStore.ts        # Chat session state & SSE read loop
│   │   └── ChatStoreProvider.tsx # React context for chat store
│   ├── generated/              # Auto-generated OpenAPI client (do not edit)
│   └── types.ts                # Shared type definitions
├── .storybook/                 # Storybook configuration
├── public/                     # Static assets for Storybook MSW
├── dist/                       # Production build output
└── package.json
```

### Key Patterns

#### State Management

- **App state** (`src/state/`) — Global stores (ChatStore) managed via React Context
- **Component state** — Local `useState` for UI state
- **Server state** — React Query for API caching, background refetch, and optimistic updates

#### SSE Streaming

Two transports share the same event vocabulary (`src/state/agentStream.ts`):

- **Chat** — Fetches SSE via `fetch()` with `ReadableStream` (manual stream handling)
- **Job live log** — Uses `EventSource` for automatic reconnection

Both route events through `dispatchAgentEvent()` to a unified handler interface.

#### API Client

The OpenAPI spec (`openapi.yaml`) is the source of truth. Generate the TypeScript client:

```bash
npm run generate
```

Pages import from `src/api.ts` which wraps the generated SDK with custom types and manual fetch for streaming endpoints.

#### Component Testing

Components are tested with Vitest + React Testing Library. Storybook stories can be tested using `@storybook/addon-vitest`.

## Development

### Prerequisites

- Node.js 20+
- npm or pnpm

### Installation

```bash
cd frontend
npm install
```

### Running the Dev Server

```bash
npm run dev
```

The dev server runs on `http://localhost:5173` and proxies `/api` and `/webhook` to `http://localhost:8000` (configured in `vite.config.ts`).

**Note:** The frontend expects a backend running on port 8000. Start it with:

```bash
docker compose up
```

### Running Tests

```bash
npm test              # Run all tests once
npm run test:watch    # Watch mode for TDD
```

Tests use Vitest + React Testing Library. Run specific tests:

```bash
npx vitest run src/test/AgentParts.test.tsx
npx vitest src/test/AgentParts.test.tsx  # Watch mode for specific file
```

### Running Storybook

```bash
npm run storybook
```

Storybook runs on `http://localhost:6006` and allows you to preview components in isolation.

### Running Storybook Tests

```bash
npx vitest --project storybook run
```

### Type Checking

```bash
npx tsc --noEmit
```

### Building for Production

```bash
npm run build
```

Output is written to `dist/` and can be served by any static file server. The Go backend embeds this directory (`//go:embed all:dist`) and serves it at `/`.

## Storybook

### Writing Stories

Stories live alongside components (e.g., `Component.tsx` + `Component.stories.tsx`).

Follow [CSF 3](https://storybook.js.org/docs/web-components/api/csf) format:

```tsx
import type { Meta, StoryObj } from '@storybook/react'
import { Component } from './Component'

const meta = {
  title: 'Components/Component',
  component: Component,
  parameters: { layout: 'centered' },
  tags: ['autodocs', 'manifest'],
} satisfies Meta<typeof Component>

export default meta
type Story = StoryObj<typeof meta>

/**
 * Default story showing the component in its base state.
 *
 * @summary base component state
 */
export const Default: Story = {
  args: { prop: 'value' },
}
```

### Storybook AI Integration

Storybook is configured with AI integration for generating stories and testing. Run:

```bash
npx storybook ai setup
```

### Storybook Best Practices

- Use `@summary` JSDoc tag for agent-friendly component descriptions
- Include `manifest` tag to expose stories to AI agents
- Keep stories focused on one use case
- Use `fn()` from `@storybook/test` for action logging
- Document props with JSDoc comments in the component source

## Contributing

- Run `npm test` before committing
- Run `npm run build` to verify no TypeScript errors
- Keep components focused and reusable
- Use Tailwind classes consistently (follow existing patterns)
- Document non-obvious behavior in component JSDoc comments
