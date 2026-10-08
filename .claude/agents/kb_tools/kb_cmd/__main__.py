#
# !INSTALLED! from the adjagent repo — do not edit in place; edit the source repo and re-install.
# !BODY-SHA256! d9ab1ac24ae3033ffbe107da44d866a1d02c5700de3e227e3715e65da10047ef
#
"""Module entry point so ``python -m kb_cmd <args>`` works."""

import sys

from .cli import main

if __name__ == "__main__":
    sys.exit(main())
