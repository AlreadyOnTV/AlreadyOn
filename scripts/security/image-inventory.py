#!/usr/bin/env python3
"""Publish only allowlisted package inventory, bound to the exact registry digest."""
import argparse
import json
from pathlib import Path
from public_inventory import image_components, inventory
from scan import validate_image


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--report', required=True, type=Path)
    parser.add_argument('--image', required=True)
    parser.add_argument('--revision', required=True)
    parser.add_argument('--output', required=True, type=Path)
    args = parser.parse_args()
    image = validate_image(args.image)
    checksum = image.rsplit('@sha256:', 1)[1]
    report = json.loads(args.report.read_text())
    bom = inventory(image, checksum, args.revision, image_components(report),
                    'Runtime OS and detected application packages; inventory only, not a vulnerability report')
    args.output.write_text(json.dumps(bom, indent=2) + '\n')
