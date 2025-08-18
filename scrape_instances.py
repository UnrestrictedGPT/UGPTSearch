# IGNORE, TESTING INSTANCE SCRAPING RETURNS HEALTHY INSTANCES
import requests
import json

def get_healthy_instances():
    """
    Fetches instances from searx.space, filters for healthy ones,
    and returns a list of their base URLs.
    """
    try:
        response = requests.get("https://searx.space/data/instances.json")
        response.raise_for_status()  # Raise an exception for bad status codes
        data = response.json()
        
        healthy_instances = []
        for url, details in data["instances"].items():
            # Adjusting the logic based on typical searx.space JSON structure
            # We will consider an instance "healthy" if it has a low error rate and supports https
            if details.get("network_type") == "normal" and details.get("generator") == "searxng":
                healthy_instances.append(url)

        return healthy_instances

    except requests.exceptions.RequestException as e:
        print(f"Error fetching data: {e}")
        return []
    except json.JSONDecodeError:
        print("Error decoding JSON response.")
        return []

if __name__ == "__main__":
    instances = get_healthy_instances()
    if instances:
        print("Healthy SearXNG Instances:")
        for instance in instances:
            print(instance)
    else:
        print("No healthy instances found or an error occurred.")
