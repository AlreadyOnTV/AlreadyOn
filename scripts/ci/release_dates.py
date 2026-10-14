"""Release names use YY-MM-DD in 2000–2099; audit timestamps remain ISO 8601."""
from datetime import date
import re

DATE = re.compile(r'\d{2}-\d{2}-\d{2}')
HISTORICAL_DATE = re.compile(r'20\d{2}-\d{2}-\d{2}')


def parse_day(value, *, historical=False):
    """Interpret two-digit years explicitly, without strptime's 1969 century pivot."""
    if historical and HISTORICAL_DATE.fullmatch(value or ''):
        value = value[2:]
    if not DATE.fullmatch(value or ''):
        raise ValueError('Release date must use YY-MM-DD')
    try:
        return date(2000 + int(value[:2]), int(value[3:5]), int(value[6:8]))
    except ValueError as error:
        raise ValueError('Release date must be a valid YY-MM-DD date') from error


def release_day(value, *, historical=False):
    """Canonicalize a validated release label; historical input is reader-only."""
    return parse_day(value, historical=historical).isoformat()[2:]
