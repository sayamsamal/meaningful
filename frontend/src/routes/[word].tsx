import { useParams } from "@solidjs/router";
import WordCard from "~/components/WordCard";

/**
 * Dynamic word page — displays the definition for any word.
 * URL: /{word}  (e.g. /apple, /meaningful, /serendipity)
 */
export default function WordPage() {
  const params = useParams<{ word: string }>();
  return <WordCard word={params.word} />;
}
