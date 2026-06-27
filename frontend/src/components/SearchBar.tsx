import {
  createSignal,
  createResource,
  createEffect,
  Show,
  For,
  onCleanup,
} from "solid-js";
import { useNavigate } from "@solidjs/router";
import SearchIcon from "../assets/search.svg?component-solid";
import CloseIcon from "../assets/close.svg?component-solid";

/**
 * Fetches autocomplete suggestions from the backend.
 * Runs client-side (no "use server") to minimize latency for
 * real-time keystroke responses.
 */
const BACKEND_URL =
  (import.meta as any).env?.VITE_BACKEND_URL ?? "http://localhost:8080";

const fetchSuggestions = async (query: string): Promise<string[]> => {
  const response = await fetch(
    `${BACKEND_URL}/api/autocomplete?query=${encodeURIComponent(query)}`
  );
  if (!response.ok) return [];
  return response.json();
};

export default function SearchBar() {
  const navigate = useNavigate();
  const [query, setQuery] = createSignal("");
  const [debouncedQuery, setDebouncedQuery] = createSignal("");
  const [showSuggestions, setShowSuggestions] = createSignal(false);
  const [activeIndex, setActiveIndex] = createSignal(-1);

  // Debounce: update debouncedQuery 200ms after the last keystroke
  let timeoutId: ReturnType<typeof setTimeout>;
  const updateQuery = (value: string) => {
    setQuery(value);
    setActiveIndex(-1);
    clearTimeout(timeoutId);

    timeoutId = setTimeout(() => setDebouncedQuery(value), 200);
  };

  // Clean up the timeout when the component unmounts
  onCleanup(() => clearTimeout(timeoutId));

  // createResource auto-fetches whenever debouncedQuery changes.
  // Returns `undefined` (skips fetch) when the source is falsy ("").
  const [suggestions] = createResource(
    () => debouncedQuery() || false,
    fetchSuggestions
  );

  // Show the dropdown whenever new suggestions arrive
  createEffect(() => {
    // Access .latest to avoid suspending this effect
    const results = suggestions.latest;
    if (results && results.length > 0) {
      setShowSuggestions(true);
    } else if (!suggestions.loading) {
      setShowSuggestions(false);
    }
  });

  /**
   * Navigate to the word's dedicated page.
   * Resets search state so the bar is clean for the next query.
   */
  const handleSelect = (word: string) => {
    setQuery("");
    setDebouncedQuery("");
    setShowSuggestions(false);
    setActiveIndex(-1);
    // Replace spaces with underscores for the URL
    const normalized = word.replace(/ /g, "_");
    navigate(`/${encodeURIComponent(normalized)}`);
  };

  // Keyboard navigation for the suggestion list
  const handleKeyDown = (e: KeyboardEvent) => {
    const items = suggestions.latest ?? [];
    if (!items.length || !showSuggestions()) return;

    switch (e.key) {
      case "ArrowDown":
        e.preventDefault();
        setActiveIndex((i) => Math.min(i + 1, items.length - 1));
        break;
      case "ArrowUp":
        e.preventDefault();
        setActiveIndex((i) => Math.max(i - 1, 0));
        break;
      case "Enter":
        e.preventDefault();
        if (activeIndex() >= 0 && activeIndex() < items.length) {
          handleSelect(items[activeIndex()]);
        }
        break;
      case "Escape":
        setShowSuggestions(false);
        setActiveIndex(-1);
        break;
    }
  };

  // Clear the input and refocus so the user can keep typing.
  const handleClear = () => {
    clearTimeout(timeoutId);
    setQuery("");
    setDebouncedQuery("");
    setShowSuggestions(false);
    setActiveIndex(-1);
    document.getElementById("search-bar-input")?.focus();
  };

  return (
    <div class="search-bar">
      <div class="search-bar__field">
        <span class="search-bar__icon" aria-hidden="true">
          <SearchIcon />
        </span>
        <input
          id="search-bar-input"
          type="text"
          class="search-bar__input"
          classList={{ "search-bar__input--loading": suggestions.loading }}
          placeholder="Search for a word..."
          value={query()}
          onInput={(e) => updateQuery(e.currentTarget.value)}
          onKeyDown={handleKeyDown}
          onBlur={() => {
            // Delay hiding to allow click events on suggestions to fire
            setTimeout(() => setShowSuggestions(false), 200);
          }}
          onFocus={() => {
            if ((suggestions.latest?.length ?? 0) > 0) setShowSuggestions(true);
          }}
          role="combobox"
          aria-expanded={showSuggestions()}
          aria-controls="search-suggestions-list"
          aria-activedescendant={
            activeIndex() >= 0 ? `suggestion-${activeIndex()}` : undefined
          }
          autocomplete="off"
        />
        <Show when={query().length > 0}>
          <button
            type="button"
            class="search-bar__clear"
            aria-label="Clear search"
            onMouseDown={(e) => e.preventDefault()}
            onClick={handleClear}
          >
            <CloseIcon aria-hidden="true" />
          </button>
        </Show>
      </div>
      <Show when={showSuggestions() && (suggestions.latest?.length ?? 0) > 0}>
        <ul
          id="search-suggestions-list"
          class="search-suggestions"
          role="listbox"
        >
          <For each={suggestions.latest!}>
            {(suggestion, index) => (
              <li
                id={`suggestion-${index()}`}
                class="search-suggestions__item"
                classList={{
                  "search-suggestions__item--active": index() === activeIndex(),
                }}
                role="option"
                aria-selected={index() === activeIndex()}
                onMouseDown={() => handleSelect(suggestion)}
              >
                {suggestion}
              </li>
            )}
          </For>
        </ul>
      </Show>
    </div>
  );
}
