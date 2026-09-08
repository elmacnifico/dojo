+The response is a Gemini generateContent envelope. The graded text is the JSON string in candidates[0].content.parts[].text; it must parse as a JSON object with "intent", "priority", and "summary" fields.
The intent must relate to a technical problem or bug report.
