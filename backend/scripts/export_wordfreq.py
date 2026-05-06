import json
from wordfreq import get_frequency_dict

def export_wordfreq(language='en', output_file='../data/wordfreq.json'):
    print(f"Exporting wordfreq data for language '{language}'...")
    
    # get_frequency_dict returns a dictionary mapping words to their zipf frequency.
    # Actually, wordfreq.get_frequency_dict returns frequency as a proportion (e.g. 0.001)
    # The user asked for "logarithmic scale (zipf_frequency) as the score".
    # Let's import zipf_frequency directly and map it.
    from wordfreq import zipf_frequency
    freq_dict = get_frequency_dict(language)
    
    zipf_dict = {}
    print(f"Loaded {len(freq_dict)} words. Calculating Zipf scores...")
    for word in freq_dict.keys():
        zipf_dict[word] = zipf_frequency(word, language)
    
    print(f"Saving to {output_file}...")
    with open(output_file, 'w', encoding='utf-8') as f:
        json.dump(zipf_dict, f, ensure_ascii=False, indent=None)
        
    print("Done!")

if __name__ == "__main__":
    export_wordfreq()
