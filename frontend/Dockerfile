FROM node:22-alpine

# Enable corepack to use pnpm
RUN corepack enable && corepack prepare pnpm@latest --activate

WORKDIR /app

# Copy dependency files
COPY package.json pnpm-lock.yaml* ./

# Install dependencies
RUN pnpm install

# Copy the rest of the application
COPY . .

# Expose the port Vite runs on
EXPOSE 5173

# Start the Vite development server and bind to all interfaces
CMD ["pnpm", "run", "dev", "--host"]
